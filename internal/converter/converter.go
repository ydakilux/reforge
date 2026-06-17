// Package converter handles the FFmpeg conversion of a single video Job,
// including output path construction, temp-file management, fallback to CPU,
// and stats updates.
package converter

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ydakilux/reforge/internal/config"
	"github.com/ydakilux/reforge/internal/database"
	"github.com/ydakilux/reforge/internal/encoder"
	"github.com/ydakilux/reforge/internal/fallback"
	"github.com/ydakilux/reforge/internal/ffmpeg"
	"github.com/ydakilux/reforge/internal/fileutil"
	"github.com/ydakilux/reforge/internal/pipeline"
	"github.com/ydakilux/reforge/internal/tui"
	"github.com/ydakilux/reforge/internal/types"
)

const (
	// OutputDirName is the top-level output directory created on the target drive.
	OutputDirName = "REFORGED"
	// TempFilePrefix is prepended to temporary files during conversion.
	TempFilePrefix = "__tmp__"
)

// Converter executes video conversion jobs.
type Converter struct {
	Config              *types.Config
	ExecDir             string
	SelectedEncoder     encoder.Encoder
	EncoderRegistry     *encoder.Registry
	FallbackManager     *fallback.FallbackManager
	DB                  database.Store
	UI                  *tui.UI
	Stats               *types.Stats
	Ctrl                *pipeline.Controller
	OutputDriveOverride string
	Log                 *logrus.Logger
	// RunID, when non-zero, is stamped onto every conversion record this
	// converter writes so they can be grouped under a single run row.
	RunID int64
}

// Process converts a single job. It is safe to call from multiple goroutines.
func (c *Converter) Process(ctx context.Context, job types.Job, dryRun bool) {
	fileName := filepath.Base(job.FilePath)

	// Debug-level: these appear in the log file but not in the TUI viewport,
	// keeping the viewport clean for completion lines only.
	c.Log.Debugf("Converting [%d/%d]: %s (%dp)", job.FileNumber, job.TotalFiles, fileName, job.Height)
	if strings.ToLower(filepath.Ext(job.FilePath)) == ".mkv" {
		c.Log.Debugf("  Format: MKV (preserving all audio/subtitle streams & metadata)")
	}

	if dryRun {
		c.Log.Infof("[DRY RUN] Would convert: %s (%dp)", fileName, job.Height)
		return
	}

	jobID := c.UI.StartJob(fileName, job.FileNumber, job.TotalFiles, job.Height, job.DurationSeconds)
	onProgress := func(pct int) { c.UI.UpdateProgress(jobID, pct) }

	// Build output paths
	baseDirName := filepath.Base(job.BaseDir)
	fileDir := filepath.Dir(job.FilePath)
	relPath, err := filepath.Rel(job.BaseDir, fileDir)
	if err != nil || relPath == "." {
		relPath = ""
	}
	var fullRelPath string
	if relPath == "" {
		fullRelPath = baseDirName
	} else {
		fullRelPath = filepath.Join(baseDirName, relPath)
	}
	sanitized := fileutil.SanitizeFolderName(fullRelPath)

	outputRoot := job.DriveRoot
	if c.OutputDriveOverride != "" {
		outputRoot = c.OutputDriveOverride
	}

	finalDir := filepath.Join(outputRoot, OutputDirName, sanitized)

	// Determine temp directory
	var tempDir string
	if c.Config.TempDirectory != "" {
		if _, err := os.Stat(c.Config.TempDirectory); err == nil {
			tempDir = filepath.Join(c.Config.TempDirectory, sanitized)
		}
	}
	if tempDir == "" {
		tempDir = filepath.Join(outputRoot, OutputDirName, "_TEMP", sanitized)
	}
	os.MkdirAll(tempDir, 0755) //nolint:errcheck

	hash8 := job.FileHash[:8]

	// Determine output extension
	inputExt := strings.ToLower(filepath.Ext(job.FilePath))
	outputExt := ".mp4"
	if inputExt == ".mkv" {
		outputExt = ".mkv"
	}

	tempPath := filepath.Join(tempDir, fmt.Sprintf("%s%s%s", TempFilePrefix, hash8, outputExt))

	// Build FFmpeg args
	qualityArgs := c.SelectedEncoder.QualityArgs(c.Config.QualityPreset, job.Width)
	deviceArgs := c.SelectedEncoder.DeviceArgs(job.GPUIndex)
	args := BuildConversionArgs(job.FilePath, tempPath, outputExt, c.Config.VideoEncoder, qualityArgs, deviceArgs, job.VideoInfo)

	// Register suspend/resume callback with the pipeline controller
	var registerSuspend func(fn func(suspend bool)) (unregister func())
	if c.Ctrl != nil {
		registerSuspend = func(fn func(suspend bool)) func() {
			id := c.Ctrl.RegisterSuspendFn(fn)
			return func() { c.Ctrl.UnregisterSuspendFn(id) }
		}
	}

	// Run FFmpeg
	ffmpegExe := config.ResolveExecutable(c.Config.FFmpegPath, config.ExeName("ffmpeg"), c.ExecDir)
	convStart := time.Now()
	rc, stderrOut := ffmpeg.Run(ctx, ffmpegExe, args, job.FilePath, job.DurationSeconds, c.Log, onProgress, registerSuspend)

	// GPU fallback
	if rc != 0 && c.Config.VideoEncoder != "libx265" {
		shouldFallback, fbErr := c.FallbackManager.HandleGPUError(stderrOut, c.SelectedEncoder, &jobStringer{job})
		if fbErr != nil {
			c.Log.Warnf("Fallback error: %v", fbErr)
		}
		if shouldFallback {
			c.Log.Info("Falling back to CPU encoder (libx265)...")
			cpuEncoder, _ := c.EncoderRegistry.Get("libx265")
			cpuQualityArgs := cpuEncoder.QualityArgs(c.Config.QualityPreset, job.Width)
			cpuArgs := BuildConversionArgs(job.FilePath, tempPath, outputExt, "libx265", cpuQualityArgs, nil, job.VideoInfo)
			rc, stderrOut = ffmpeg.Run(ctx, ffmpegExe, cpuArgs, job.FilePath, job.DurationSeconds, c.Log, onProgress, registerSuspend)
		}
	}

	if rc != 0 {
		c.Log.Errorf("FFmpeg failed with exit code %d for %s", rc, job.FilePath)
		if stderrOut != "" {
			c.Log.Errorf("FFmpeg stderr: %s", stderrOut)
		}
		c.UI.CompleteError(jobID, fmt.Sprintf("✗ FAILED  [%d/%d] %s", job.FileNumber, job.TotalFiles, fileName))
		if err := c.DB.UpdateRecord(ctx, job.DriveRoot, job.FileHash, types.Record{
			OriginalSize:    job.OriginalSize,
			Error:           fmt.Sprintf("rc_%d", rc),
			SourceCodec:     job.CodecID,
			SourceContainer: strings.ToLower(filepath.Ext(job.FilePath)),
			SourcePath:      job.FilePath,
			Width:           job.Width,
			Height:          job.Height,
			DurationSecs:    job.DurationSeconds,
			ConvertedAt:     time.Now().UTC().Format(time.RFC3339),
			RunID:           c.RunID,
		}); err != nil {
			c.Log.Errorf("Failed to update error record for %s: %v", job.FilePath, err)
		}
		os.Remove(tempPath) //nolint:errcheck
		c.Stats.IncrFilesErrored()
		return
	}

	// Stat the temp output
	tempInfo, err := os.Stat(tempPath)
	if err != nil {
		c.Log.Errorf("Failed to stat temp file: %v", err)
		c.UI.CompleteError(jobID, fmt.Sprintf("✗ ERROR   [%d/%d] %s", job.FileNumber, job.TotalFiles, fileName))
		os.Remove(tempPath) //nolint:errcheck
		c.Stats.IncrFilesErrored()
		return
	}

	newSize := tempInfo.Size()
	origMB := float64(job.OriginalSize) / (1024 * 1024)
	newMB := float64(newSize) / (1024 * 1024)

	if newSize < job.OriginalSize {
		// KEPT
		reduction := float64(job.OriginalSize-newSize) / float64(job.OriginalSize) * 100
		savedMB := origMB - newMB
		summary := fmt.Sprintf("✓ KEPT    [%d/%d] %-32s  %.2f MB → %.2f MB  (-%.2f MB, %.1f%%)",
			job.FileNumber, job.TotalFiles, fileutil.TruncateString(fileName, 32), origMB, newMB, savedMB, reduction)
		// Log to file only (TUI gets it via CompleteKept; logging here would duplicate).
		c.Log.Infof("%s%s", tui.FileOnlyPrefix, summary)
		c.UI.CompleteKept(jobID, summary)

		os.MkdirAll(finalDir, 0755) //nolint:errcheck

		baseName := strings.TrimSuffix(filepath.Base(job.FilePath), filepath.Ext(job.FilePath))
		finalPath := filepath.Join(finalDir, baseName+outputExt)
		if _, err := os.Stat(finalPath); err == nil {
			finalPath = filepath.Join(finalDir, fmt.Sprintf("%s__%s%s", baseName, hash8, outputExt))
		}

		if err := MoveFile(tempPath, finalPath); err != nil {
			c.Log.Errorf("Failed to move file to final location: %v", err)
			os.Remove(tempPath) //nolint:errcheck
			return
		}

		if err := c.DB.UpdateRecord(ctx, job.DriveRoot, job.FileHash, types.Record{
			OriginalSize:           job.OriginalSize,
			ConvertedSize:          newSize,
			Output:                 finalPath,
			SourceCodec:            job.CodecID,
			SourceContainer:        strings.ToLower(filepath.Ext(job.FilePath)),
			SourcePath:             job.FilePath,
			Width:                  job.Width,
			Height:                 job.Height,
			DurationSecs:           job.DurationSeconds,
			ConvertedAt:            time.Now().UTC().Format(time.RFC3339),
			ConversionDurationSecs: time.Since(convStart).Seconds(),
			RunID:                  c.RunID,
		}); err != nil {
			c.Log.Errorf("Failed to update record for %s: %v", job.FilePath, err)
		}

		c.Stats.AddConverted(true, job.OriginalSize, newSize)
	} else {
		// DISCARDED
		increase := float64(newSize-job.OriginalSize) / float64(job.OriginalSize) * 100
		increasedMB := newMB - origMB
		summary := fmt.Sprintf("✗ DISCARD [%d/%d] %-32s  %.2f MB → %.2f MB  (+%.2f MB, +%.1f%%)",
			job.FileNumber, job.TotalFiles, fileutil.TruncateString(fileName, 32), origMB, newMB, increasedMB, increase)
		// Log to file only (TUI gets it via CompleteDiscard; logging here would duplicate).
		c.Log.Infof("%s%s", tui.FileOnlyPrefix, summary)
		c.UI.CompleteDiscard(jobID, summary)

		os.Remove(tempPath) //nolint:errcheck

		if err := c.DB.UpdateRecord(ctx, job.DriveRoot, job.FileHash, types.Record{
			OriginalSize:           job.OriginalSize,
			ConvertedSize:          newSize,
			Note:                   "not_beneficial",
			SourceCodec:            job.CodecID,
			SourceContainer:        strings.ToLower(filepath.Ext(job.FilePath)),
			SourcePath:             job.FilePath,
			Width:                  job.Width,
			Height:                 job.Height,
			DurationSecs:           job.DurationSeconds,
			ConvertedAt:            time.Now().UTC().Format(time.RFC3339),
			ConversionDurationSecs: time.Since(convStart).Seconds(),
			RunID:                  c.RunID,
		}); err != nil {
			c.Log.Errorf("Failed to update record for %s: %v", job.FilePath, err)
		}

		c.Stats.AddConverted(false, job.OriginalSize, job.OriginalSize)
	}
}

// mpegTSInputExts is the set of input container extensions that produce
// ADTS-framed AAC packets. When muxing such streams into MP4 with -c:a copy,
// ffmpeg implicitly inserts the aac_adtstoasc bitstream filter, which aborts
// the whole job on the first corrupt ADTS header — and .ts/.m2ts captures
// are routinely slightly corrupt (PES mismatch, dropped audio packets).
// For these inputs we always re-encode AAC instead of copying, which
// bypasses the bitstream filter and lets ffmpeg conceal audio glitches.
var mpegTSInputExts = map[string]bool{
	".ts": true, ".m2ts": true, ".mts": true, ".m2t": true,
}

// mp4CompatibleAudioCodecs is the set of audio codecs that can be stored in an
// MP4 container without re-encoding. All other codecs (e.g. dts, truehd, mlp)
// must be transcoded to AAC.
var mp4CompatibleAudioCodecs = map[string]bool{
	"aac": true, "mp3": true, "ac3": true, "eac3": true, "opus": true, "flac": true,
}

// mp4CompatibleSubtitleCodecs is the set of subtitle codecs that can be
// carried in an MP4 container. Image-based formats (pgssub, dvd_subtitle,
// dvb_subtitle) are not supported and must be dropped.
var mp4CompatibleSubtitleCodecs = map[string]bool{
	"mov_text": true, "subrip": true, "srt": true, "webvtt": true, "tx3g": true,
}

// hdrColorTransfers identifies HDR transfer characteristics that warrant
// explicit colour-metadata passthrough flags.
var hdrColorTransfers = map[string]bool{
	"smpte2084": true, "arib-std-b67": true, "bt2020-10": true, "bt2020-12": true,
}

// BuildConversionArgs constructs FFmpeg arguments for a single conversion job.
//
// For MKV output all streams are copied verbatim with full metadata.
// For MP4 output:
//   - Audio streams are copied when their codec is MP4-compatible; otherwise
//     they are transcoded to AAC.
//   - Subtitle streams are copied when MP4-compatible; image-based subtitles
//     (PGS, DVDSUB) are silently dropped because the MP4 container cannot
//     carry them.
//   - HDR colour-space metadata from the source video stream is forwarded via
//     explicit FFmpeg colour flags so that HDR10 content is not tone-mapped.
//
// videoInfo may be nil (e.g. when called from the GPU-fallback path where info
// is already available on the Job); the function degrades gracefully.
func BuildConversionArgs(inputPath, outputPath, outputExt, encoderName string, qualityArgs, deviceArgs []string, videoInfo *types.VideoInfo) []string {
	args := []string{
		"-hide_banner", "-y", "-nostats",
		"-progress", "pipe:1",
		"-i", inputPath,
	}

	if len(deviceArgs) > 0 {
		args = append(args, deviceArgs...)
	}

	args = append(args, "-c:v", encoderName)
	args = append(args, qualityArgs...)

	if outputExt == ".mkv" {
		// MKV: copy everything, keep all metadata and chapters.
		args = append(args,
			"-map", "0",
			"-c:a", "copy",
			"-c:s", "copy",
			"-map_metadata", "0",
			"-map_chapters", "0",
		)
	} else {
		args = append(args, buildMP4Args(videoInfo, inputPath)...)
	}

	args = append(args, outputPath)
	return args
}

// buildMP4Args returns the stream-mapping, audio/subtitle codec, HDR
// passthrough, and movflags arguments specific to the MP4 container.
//
// inputPath is consulted to detect MPEG-TS sources (.ts/.m2ts/...), which
// force AAC re-encoding instead of stream copy — see mpegTSInputExts for the
// rationale (corrupt ADTS frames break the aac_adtstoasc bitstream filter
// otherwise, failing the whole job at mux time).
func buildMP4Args(videoInfo *types.VideoInfo, inputPath string) []string {
	var args []string

	args = append(args, "-map", "0:v:0") // primary video

	forceAACReencode := mpegTSInputExts[strings.ToLower(filepath.Ext(inputPath))]

	if videoInfo != nil && len(videoInfo.AudioStreams) > 0 {
		if forceAACReencode {
			// MPEG-TS input: re-encode every audio stream to AAC to bypass
			// the aac_adtstoasc bitstream filter, which fails fatally on
			// corrupt ADTS headers commonly found in .ts captures.
			for i := range videoInfo.AudioStreams {
				args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
			}
			args = append(args, "-c:a", "aac")
		} else {
			// Per-stream audio disposition: copy compatible, transcode others.
			hasCompatible := false
			for i, a := range videoInfo.AudioStreams {
				if mp4CompatibleAudioCodecs[strings.ToLower(a.CodecName)] {
					args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
					hasCompatible = true
				}
			}
			if hasCompatible {
				args = append(args, "-c:a", "copy")
			}
			for i, a := range videoInfo.AudioStreams {
				if !mp4CompatibleAudioCodecs[strings.ToLower(a.CodecName)] {
					streamSpec := fmt.Sprintf("0:a:%d", i)
					args = append(args, "-map", streamSpec)
					args = append(args, fmt.Sprintf("-c:a:%d", i), "aac")
				}
			}
			if !hasCompatible {
				args = append(args, "-c:a", "aac")
			}
		}
	} else {
		// No probed audio info: copy everything (or AAC re-encode for TS,
		// since we can't know whether the audio is ADTS-AAC and copying
		// would risk the same failure mode).
		if forceAACReencode {
			args = append(args, "-map", "0:a?", "-c:a", "aac")
		} else {
			args = append(args, "-map", "0:a?", "-c:a", "copy")
		}
	}

	// Subtitles: copy only MP4-compatible text-based formats.
	if videoInfo != nil && len(videoInfo.SubtitleStreams) > 0 {
		for i, s := range videoInfo.SubtitleStreams {
			if mp4CompatibleSubtitleCodecs[strings.ToLower(s.CodecName)] {
				args = append(args, "-map", fmt.Sprintf("0:s:%d", i))
			}
		}
		args = append(args, "-c:s", "mov_text")
	}

	// HDR colour-space passthrough.
	if videoInfo != nil && hdrColorTransfers[strings.ToLower(videoInfo.Color.ColorTransfer)] {
		if videoInfo.Color.ColorPrimaries != "" {
			args = append(args, "-color_primaries", videoInfo.Color.ColorPrimaries)
		}
		if videoInfo.Color.ColorTransfer != "" {
			args = append(args, "-color_trc", videoInfo.Color.ColorTransfer)
		}
		if videoInfo.Color.ColorSpace != "" {
			args = append(args, "-colorspace", videoInfo.Color.ColorSpace)
		}
	}

	args = append(args, "-map_metadata", "0", "-map_chapters", "0")
	args = append(args, "-movflags", "+faststart")
	return args
}

// MoveFile moves src to dst. Tries os.Rename first (atomic, same-drive),
// then falls back to copy+delete for cross-volume moves.
func MoveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("moveFile open src: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		in.Close()
		return fmt.Errorf("moveFile mkdir dst: %w", err)
	}

	dstTmp := dst + ".tmp"
	out, err := os.Create(dstTmp)
	if err != nil {
		in.Close()
		return fmt.Errorf("moveFile create dst tmp: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		in.Close()
		os.Remove(dstTmp) //nolint:errcheck
		return fmt.Errorf("moveFile copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		in.Close()
		os.Remove(dstTmp) //nolint:errcheck
		return fmt.Errorf("moveFile sync: %w", err)
	}
	out.Close()
	in.Close()

	if err := os.Rename(dstTmp, dst); err != nil {
		os.Remove(dstTmp) //nolint:errcheck
		return fmt.Errorf("moveFile rename dst tmp: %w", err)
	}
	if err := os.Remove(src); err != nil {
		// Log is not available here — caller logs if needed.
		_ = err
	}
	return nil
}

// jobStringer wraps a types.Job to implement fmt.Stringer for FallbackManager.
type jobStringer struct {
	job types.Job
}

func (js *jobStringer) String() string {
	return filepath.Base(js.job.FilePath)
}
