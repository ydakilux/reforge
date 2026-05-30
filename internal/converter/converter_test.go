package converter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydakilux/reforge/internal/types"
)

// ── Test helpers ────────────────────────────────────────────────────────────

func assertContains(t *testing.T, args []string, value string) {
	t.Helper()
	for _, a := range args {
		if a == value {
			return
		}
	}
	t.Errorf("args missing %q\nargs: %s", value, strings.Join(args, " "))
}

func assertNotContains(t *testing.T, args []string, value string) {
	t.Helper()
	for _, a := range args {
		if a == value {
			t.Errorf("args should NOT contain %q\nargs: %s", value, strings.Join(args, " "))
			return
		}
	}
}

func assertContainsSequence(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Errorf("args missing consecutive sequence %q %q\nargs: %s", key, value, strings.Join(args, " "))
}

func assertNotContainsSequence(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			t.Errorf("args should NOT contain consecutive sequence %q %q\nargs: %s", key, value, strings.Join(args, " "))
			return
		}
	}
}

// ── BuildConversionArgs tests ───────────────────────────────────────────────

func TestBuildConversionArgs_BasicMP4_NilVideoInfo(t *testing.T) {
	args := BuildConversionArgs("input.mp4", "output.mp4", ".mp4", "libx265", []string{"-crf", "28"}, nil, nil)

	assertContainsSequence(t, args, "-i", "input.mp4")
	assertContainsSequence(t, args, "-c:v", "libx265")
	assertContainsSequence(t, args, "-map", "0:v:0")
	assertContainsSequence(t, args, "-map", "0:a?")
	assertContainsSequence(t, args, "-c:a", "copy")
	assertContainsSequence(t, args, "-movflags", "+faststart")

	if args[len(args)-1] != "output.mp4" {
		t.Errorf("last arg should be output path, got %q", args[len(args)-1])
	}
}

func TestBuildConversionArgs_MKV(t *testing.T) {
	args := BuildConversionArgs("input.mkv", "output.mkv", ".mkv", "libx265", []string{"-crf", "22"}, nil, nil)

	assertContainsSequence(t, args, "-map", "0")
	assertContainsSequence(t, args, "-c:a", "copy")
	assertContainsSequence(t, args, "-c:s", "copy")
	assertContainsSequence(t, args, "-map_metadata", "0")
	assertContainsSequence(t, args, "-map_chapters", "0")
	assertNotContains(t, args, "-movflags")
	assertNotContains(t, args, "+faststart")
}

func TestBuildConversionArgs_MP4CompatibleAudio(t *testing.T) {
	tests := []struct {
		name  string
		codec string
	}{
		{"aac", "aac"},
		{"mp3", "mp3"},
		{"ac3", "ac3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vi := &types.VideoInfo{
				AudioStreams: []types.AudioStream{
					{CodecName: tt.codec, Channels: 2},
				},
			}
			args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "libx265", nil, nil, vi)

			assertContainsSequence(t, args, "-map", "0:a:0")
			assertContainsSequence(t, args, "-c:a", "copy")
			assertNotContainsSequence(t, args, "-c:a:0", "aac")
		})
	}
}

func TestBuildConversionArgs_MP4IncompatibleAudio_DTS(t *testing.T) {
	vi := &types.VideoInfo{
		AudioStreams: []types.AudioStream{
			{CodecName: "dts", Channels: 6},
		},
	}
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "libx265", nil, nil, vi)

	assertContainsSequence(t, args, "-map", "0:a:0")
	assertContainsSequence(t, args, "-c:a:0", "aac")
	assertContainsSequence(t, args, "-c:a", "aac")
}

func TestBuildConversionArgs_MP4MixedAudio(t *testing.T) {
	vi := &types.VideoInfo{
		AudioStreams: []types.AudioStream{
			{CodecName: "aac", Channels: 2},
			{CodecName: "dts", Channels: 6},
		},
	}
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "libx265", nil, nil, vi)

	assertContainsSequence(t, args, "-c:a", "copy")
	assertContainsSequence(t, args, "-c:a:1", "aac")
	assertContainsSequence(t, args, "-movflags", "+faststart")
}

func TestBuildConversionArgs_MP4CompatibleSubtitles(t *testing.T) {
	vi := &types.VideoInfo{
		AudioStreams: []types.AudioStream{
			{CodecName: "aac", Channels: 2},
		},
		SubtitleStreams: []types.SubtitleStream{
			{CodecName: "subrip"},
		},
	}
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "libx265", nil, nil, vi)

	assertContainsSequence(t, args, "-map", "0:s:0")
	assertContainsSequence(t, args, "-c:s", "mov_text")
}

func TestBuildConversionArgs_MP4ImageSubtitlesDropped(t *testing.T) {
	vi := &types.VideoInfo{
		AudioStreams: []types.AudioStream{
			{CodecName: "aac", Channels: 2},
		},
		SubtitleStreams: []types.SubtitleStream{
			{CodecName: "pgssub"},
		},
	}
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "libx265", nil, nil, vi)

	assertNotContainsSequence(t, args, "-map", "0:s:0")
	assertContainsSequence(t, args, "-c:s", "mov_text")
}

func TestBuildConversionArgs_HDRContent(t *testing.T) {
	vi := &types.VideoInfo{
		AudioStreams: []types.AudioStream{
			{CodecName: "aac", Channels: 2},
		},
		Color: types.ColorInfo{
			ColorPrimaries: "bt2020",
			ColorTransfer:  "smpte2084",
			ColorSpace:     "bt2020nc",
		},
	}
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "hevc_nvenc", nil, nil, vi)

	assertContainsSequence(t, args, "-color_primaries", "bt2020")
	assertContainsSequence(t, args, "-color_trc", "smpte2084")
	assertContainsSequence(t, args, "-colorspace", "bt2020nc")
}

func TestBuildConversionArgs_NonHDRContent(t *testing.T) {
	vi := &types.VideoInfo{
		AudioStreams: []types.AudioStream{
			{CodecName: "aac", Channels: 2},
		},
		Color: types.ColorInfo{
			ColorPrimaries: "bt709",
			ColorTransfer:  "bt709",
			ColorSpace:     "bt709",
		},
	}
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "libx265", nil, nil, vi)

	assertNotContains(t, args, "-color_primaries")
	assertNotContains(t, args, "-color_trc")
	assertNotContains(t, args, "-colorspace")
}

func TestBuildConversionArgs_WithDeviceArgs(t *testing.T) {
	deviceArgs := []string{"-gpu", "1", "-init_hw_device", "cuda=cu:1"}
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "hevc_nvenc", nil, deviceArgs, nil)

	assertContains(t, args, "-gpu")
	assertContainsSequence(t, args, "-gpu", "1")
	assertContains(t, args, "-init_hw_device")

	gpuIdx := -1
	cvIdx := -1
	for i, a := range args {
		if a == "-gpu" && gpuIdx == -1 {
			gpuIdx = i
		}
		if a == "-c:v" {
			cvIdx = i
		}
	}
	if gpuIdx >= cvIdx {
		t.Errorf("device args should appear before -c:v; -gpu at %d, -c:v at %d", gpuIdx, cvIdx)
	}
}

func TestBuildConversionArgs_WithQualityArgs(t *testing.T) {
	qualityArgs := []string{"-crf", "23", "-preset", "slow"}
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "libx265", qualityArgs, nil, nil)

	assertContainsSequence(t, args, "-crf", "23")
	assertContainsSequence(t, args, "-preset", "slow")

	cvIdx := -1
	crfIdx := -1
	for i, a := range args {
		if a == "-c:v" {
			cvIdx = i
		}
		if a == "-crf" {
			crfIdx = i
		}
	}
	if crfIdx <= cvIdx {
		t.Errorf("quality args should appear after -c:v; -c:v at %d, -crf at %d", cvIdx, crfIdx)
	}
}

func TestBuildConversionArgs_CommonPrefixArgs(t *testing.T) {
	args := BuildConversionArgs("in.mp4", "out.mp4", ".mp4", "libx265", nil, nil, nil)

	assertContains(t, args, "-hide_banner")
	assertContains(t, args, "-y")
	assertContains(t, args, "-nostats")
	assertContainsSequence(t, args, "-progress", "pipe:1")
}

// ── MoveFile tests ──────────────────────────────────────────────────────────

func TestMoveFile_SameVolume(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "dest.txt")

	content := []byte("hello move test")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("setup: write source: %v", err)
	}

	if err := MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source file should not exist after move")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("dest content = %q, want %q", got, content)
	}
}

func TestMoveFile_SourceNotExist(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "nonexistent.txt")
	dst := filepath.Join(dir, "dest.txt")

	err := MoveFile(src, dst)
	if err == nil {
		t.Fatal("MoveFile should return error when source does not exist")
	}
}

func TestMoveFile_CreatesNestedDirs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "a", "b", "c", "dest.txt")

	content := []byte("nested dir test")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("setup: write source: %v", err)
	}

	if err := MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("dest content = %q, want %q", got, content)
	}
}

func TestMoveFile_SourceRemovedAfterMove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "removal_test.txt")
	dst := filepath.Join(dir, "sub", "removal_dest.txt")

	content := []byte("removal verification")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("setup: write source: %v", err)
	}

	if err := MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source file should be removed after successful move")
	}
}

func TestMoveFile_LargerContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "large.bin")
	dst := filepath.Join(dir, "deep", "nested", "large.bin")

	content := make([]byte, 64*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("setup: write source: %v", err)
	}

	if err := MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if len(got) != len(content) {
		t.Fatalf("dest size = %d, want %d", len(got), len(content))
	}
	for i := range content {
		if got[i] != content[i] {
			t.Fatalf("dest content differs at byte %d: got %d, want %d", i, got[i], content[i])
		}
	}
}

// ── MPEG-TS input handling ──────────────────────────────────────────────────
//
// MPEG-TS sources (.ts/.m2ts/...) carry ADTS-framed AAC. Copying that stream
// into MP4 invokes the aac_adtstoasc bitstream filter, which fatally aborts
// the whole conversion when it hits a corrupt ADTS header — a common
// occurrence in IPTV/DVB captures. Reforge must force an AAC re-encode for
// these inputs to bypass the filter.

func TestBuildConversionArgs_MPEGTSInput_ForcesAACReencode(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"ts", "input.ts"},
		{"ts_uppercase", "input.TS"},
		{"m2ts", "input.m2ts"},
		{"mts", "input.mts"},
		{"m2t", "input.m2t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vi := &types.VideoInfo{
				AudioStreams: []types.AudioStream{
					{CodecName: "aac", Channels: 2}, // would normally be copied
				},
			}
			args := BuildConversionArgs(tt.in, "out.mp4", ".mp4", "libx265", nil, nil, vi)

			assertContainsSequence(t, args, "-map", "0:a:0")
			assertContainsSequence(t, args, "-c:a", "aac")
			assertNotContainsSequence(t, args, "-c:a", "copy")
		})
	}
}

func TestBuildConversionArgs_MPEGTSInput_NilVideoInfo(t *testing.T) {
	// Without probed audio info we still must avoid -c:a copy for TS inputs.
	args := BuildConversionArgs("input.ts", "out.mp4", ".mp4", "libx265", nil, nil, nil)

	assertContainsSequence(t, args, "-map", "0:a?")
	assertContainsSequence(t, args, "-c:a", "aac")
	assertNotContainsSequence(t, args, "-c:a", "copy")
}

func TestBuildConversionArgs_MPEGTSInput_MultipleAACStreams(t *testing.T) {
	vi := &types.VideoInfo{
		AudioStreams: []types.AudioStream{
			{CodecName: "aac", Channels: 2},
			{CodecName: "aac", Channels: 6},
		},
	}
	args := BuildConversionArgs("input.ts", "out.mp4", ".mp4", "libx265", nil, nil, vi)

	assertContainsSequence(t, args, "-map", "0:a:0")
	assertContainsSequence(t, args, "-map", "0:a:1")
	assertContainsSequence(t, args, "-c:a", "aac")
	assertNotContainsSequence(t, args, "-c:a", "copy")
}

func TestBuildConversionArgs_NonTSInput_StillCopiesAAC(t *testing.T) {
	// Regression guard: the TS special-case must not affect normal containers.
	vi := &types.VideoInfo{
		AudioStreams: []types.AudioStream{
			{CodecName: "aac", Channels: 2},
		},
	}
	args := BuildConversionArgs("input.mkv", "out.mp4", ".mp4", "libx265", nil, nil, vi)

	assertContainsSequence(t, args, "-c:a", "copy")
}
