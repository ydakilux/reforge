package encoder

import (
	"strconv"
	"strings"
)

var nvencCQTable = qualityTable{
	{20, 22, 24, 26}, // high_quality
	{24, 26, 28, 30}, // balanced
	{28, 30, 32, 35}, // space_saver
}

// NvencEncoder implements Encoder for the hevc_nvenc (NVIDIA NVENC) codec.
type NvencEncoder struct{}

// NewNvencEncoder creates a new NVENC encoder instance.
func NewNvencEncoder() *NvencEncoder {
	return &NvencEncoder{}
}

// Name returns the FFmpeg codec name for NVIDIA NVENC.
func (e *NvencEncoder) Name() string {
	return "hevc_nvenc"
}

// QualityArgs returns NVENC quality arguments (-cq and -preset) for the given preset and resolution.
func (e *NvencEncoder) QualityArgs(preset string, width int) []string {
	cq := strconv.Itoa(qualityValue(preset, width, nvencCQTable))
	return []string{"-cq", cq, "-preset", nvencPreset(preset)}
}

// DeviceArgs returns the -gpu flag to select a specific NVIDIA GPU by index.
func (e *NvencEncoder) DeviceArgs(gpuIndex int) []string {
	return []string{"-gpu", strconv.Itoa(gpuIndex)}
}

// IsAvailable trial-encodes a short clip to verify NVENC works with the given FFmpeg binary.
func (e *NvencEncoder) IsAvailable(ffmpegPath string) bool {
	return trialEncode(ffmpegPath, "hevc_nvenc")
}

// ParseError inspects FFmpeg stderr for NVENC-specific GPU errors and returns a human-readable message.
func (e *NvencEncoder) ParseError(stderr string) (bool, string) {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "no capable devices found") {
		return true, "NVENC: no capable GPU devices"
	}
	if strings.Contains(lower, "openencodesessionex failed") {
		return true, "NVENC: session limit or memory error"
	}
	if strings.Contains(lower, "initializeencoder failed") {
		return true, "NVENC: encoder initialization failed"
	}
	// Driver too old: FFmpeg built against a newer NVENC SDK than the installed driver.
	// FFmpeg 9+ requires NVENC SDK >= 11.1 (API 13.1) and NVIDIA driver >= 610.
	// e.g. "Driver does not support the required nvenc API version. Required: 13.1 Found: 13.0"
	// e.g. "nvenc sdk version X.Y is not supported" (FFmpeg 9+)
	if strings.Contains(lower, "does not support the required nvenc api version") ||
		strings.Contains(lower, "nvenc sdk version") ||
		strings.Contains(lower, "minimum required nvidia driver") ||
		strings.Contains(lower, "error while opening encoder") {
		return true, "NVENC: driver too old — update NVIDIA driver to ≥ 610 to use GPU encoding with FFmpeg 9"
	}
	return false, ""
}

func nvencPreset(preset string) string {
	switch strings.ToLower(preset) {
	case "high_quality":
		return "p7"
	case "space_saver":
		return "p4"
	default:
		return "p5"
	}
}
