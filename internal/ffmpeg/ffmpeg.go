// Package ffmpeg wraps FFmpeg and FFprobe execution, progress parsing, and
// process suspend/resume on Windows.
package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ydakilux/reforge/internal/types"
)

const (
	// ExitCodeInternal is the synthetic exit code returned when FFmpeg cannot be started or its process state is unknown.
	ExitCodeInternal = 999
	// FFprobeTimeout is the maximum time allowed for an FFprobe duration query.
	FFprobeTimeout = 30 * time.Second
)

// MakeSuspendFn returns a suspend/resume callback for the given *exec.Cmd.
// Pass true to suspend, false to resume. Safe to call before the process starts
// (it checks cmd.Process for nil).
func MakeSuspendFn(cmd *exec.Cmd) func(suspend bool) {
	return func(suspend bool) {
		if cmd.Process == nil {
			return
		}
		if suspend {
			suspendProcess(cmd.Process.Pid)
		} else {
			resumeProcess(cmd.Process.Pid)
		}
	}
}

// Run executes FFmpeg with the given args. onProgress is called with the
// percentage (0-100) each time it changes; pass nil to disable callbacks.
// registerSuspend, if non-nil, is called with a suspend/resume function once
// the process starts — the caller should unregister it when Run returns.
func Run(ctx context.Context, ffmpegExe string, args []string, filePath string, totalDuration float64, logger *logrus.Logger, onProgress func(pct int), registerSuspend func(fn func(suspend bool)) (unregister func())) (int, string) {
	cmd := exec.CommandContext(ctx, ffmpegExe, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Errorf("Failed to create stdout pipe: %v", err)
		return ExitCodeInternal, ""
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		logger.Errorf("Failed to create stderr pipe: %v", err)
		return ExitCodeInternal, ""
	}

	if err := cmd.Start(); err != nil {
		logger.Errorf("Failed to start FFmpeg: %v", err)
		return ExitCodeInternal, ""
	}

	// Register suspend/resume callback now that the process is running.
	if registerSuspend != nil {
		unregister := registerSuspend(MakeSuspendFn(cmd))
		defer unregister()
	}

	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		errScanner := bufio.NewScanner(stderr)
		corruptErrorCount := 0
		killed := false
		for errScanner.Scan() {
			line := errScanner.Text()
			stderrBuf.WriteString(line)
			stderrBuf.WriteByte('\n')

			if !killed {
				lower := strings.ToLower(line)
				if strings.Contains(lower, "invalid nal unit size") ||
					strings.Contains(lower, "error splitting the input into nal units") ||
					strings.Contains(lower, "error submitting packet to decoder") ||
					strings.Contains(lower, "corrupt input packet") {
					corruptErrorCount++
					if corruptErrorCount >= 2 {
						if logger != nil {
							logger.Errorf("Corrupt stream detected in %s — stopping conversion immediately to skip file", filePath)
						}
						killed = true
						killProcess(cmd)
					}
				}
			}
		}
	}()

	lastPct := -1
	var lastSent time.Time
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "out_time_ms=") || strings.HasPrefix(line, "out_time_us=") || strings.HasPrefix(line, "out_time=") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				var outTime float64
				if strings.HasPrefix(line, "out_time_ms=") {
					if val, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
						outTime = float64(val) / 1000000.0
					}
				} else if strings.HasPrefix(line, "out_time_us=") {
					if val, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
						outTime = float64(val) / 1000000.0
					}
				} else if strings.HasPrefix(line, "out_time=") {
					timeParts := strings.Split(parts[1], ":")
					if len(timeParts) == 3 {
						h, _ := strconv.Atoi(timeParts[0])
						m, _ := strconv.Atoi(timeParts[1])
						s, _ := strconv.ParseFloat(timeParts[2], 64)
						outTime = float64(h*3600+m*60) + s
					}
				}

				if totalDuration > 0 && outTime > 0 {
					pct := int(100 * outTime / totalDuration)
					if pct > lastPct {
						lastPct = pct
						if onProgress != nil {
							now := time.Now()
							if pct >= 100 || lastSent.IsZero() || now.Sub(lastSent) >= 2500*time.Millisecond {
								lastSent = now
								onProgress(pct)
							}
						}
					}
				}
			}
		}
	}

	<-stderrDone

	if err := cmd.Wait(); err != nil {
		stderrStr := stderrBuf.String()
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), stderrStr
		}
		killProcess(cmd)
		return ExitCodeInternal, stderrStr
	}

	return 0, stderrBuf.String()
}

// GetDuration runs FFprobe to extract the total duration (in seconds) of filePath. Returns 0 if FFprobe is unavailable or the query fails.
func GetDuration(filePath, ffprobeExe string, logger *logrus.Logger) float64 {
	if ffprobeExe == "" {
		return 0.0
	}

	ctx, cancel := context.WithTimeout(context.Background(), FFprobeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffprobeExe, "-v", "error", "-show_entries", "format=duration", "-of", "json", filePath)
	output, err := cmd.Output()
	if err != nil {
		return 0.0
	}

	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return 0.0
	}

	duration, _ := strconv.ParseFloat(result.Format.Duration, 64)
	return duration
}

// GetMediaInfo probes all streams (video, audio, subtitle) in a file and
// returns a VideoInfo struct populated with codec, geometry, HDR colour
// metadata, and per-stream audio/subtitle details.
func GetMediaInfo(filePath, ffprobeExe string) (*types.VideoInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), FFprobeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobeExe,
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		filePath,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var result struct {
		Streams []struct {
			CodecType      string `json:"codec_type"`
			CodecName      string `json:"codec_name"`
			CodecTag       string `json:"codec_tag_string"`
			Width          int    `json:"width"`
			Height         int    `json:"height"`
			ColorPrimaries string `json:"color_primaries"`
			ColorTransfer  string `json:"color_transfer"`
			ColorSpace     string `json:"color_space"`
			Channels       int    `json:"channels"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, err
	}

	info := &types.VideoInfo{}
	videoFound := false

	for _, s := range result.Streams {
		switch s.CodecType {
		case "video":
			if !videoFound {
				info.Format = s.CodecName
				info.CodecID = s.CodecTag
				info.Width = s.Width
				info.Height = s.Height
				info.Color = types.ColorInfo{
					ColorPrimaries: s.ColorPrimaries,
					ColorTransfer:  s.ColorTransfer,
					ColorSpace:     s.ColorSpace,
				}
				videoFound = true
			}
		case "audio":
			info.AudioStreams = append(info.AudioStreams, types.AudioStream{
				CodecName: s.CodecName,
				Channels:  s.Channels,
			})
		case "subtitle":
			info.SubtitleStreams = append(info.SubtitleStreams, types.SubtitleStream{
				CodecName: s.CodecName,
			})
		}
	}

	if !videoFound {
		return nil, fmt.Errorf("no video stream found")
	}
	return info, nil
}

// IsHEVC reports whether the given codec name or tag identifies an HEVC/H.265
// stream. Recognises codec_name "hevc", the common MP4 tag "hvc1", and any
// string that contains "HEVC" or "HVC1" (case-insensitive).
func IsHEVC(format, codecID string) bool {
	fUpper := strings.ToUpper(format)
	cUpper := strings.ToUpper(codecID)
	return fUpper == "HEVC" ||
		strings.Contains(fUpper, "HEVC") ||
		cUpper == "HVC1" ||
		strings.Contains(cUpper, "HVC1")
}
