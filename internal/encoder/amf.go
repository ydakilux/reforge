package encoder

import (
	"strconv"
	"strings"
)

var amfQPTable = qualityTable{
	{16, 18, 20, 22}, // high_quality
	{20, 22, 24, 27}, // balanced
	{24, 26, 28, 31}, // space_saver
}

// AmfEncoder implements Encoder for the AMD AMF (hevc_amf) codec.
type AmfEncoder struct{}

// NewAmfEncoder creates a new AMD AMF encoder instance.
func NewAmfEncoder() *AmfEncoder {
	return &AmfEncoder{}
}

// Name returns the FFmpeg codec name for AMD AMF.
func (e *AmfEncoder) Name() string {
	return "hevc_amf"
}

// QualityArgs returns AMF quality arguments (-rc cqp, -qp_i/-qp_p, -quality) for the given preset and resolution.
func (e *AmfEncoder) QualityArgs(preset string, width int) []string {
	qp := strconv.Itoa(qualityValue(preset, width, amfQPTable))
	return []string{"-rc", "cqp", "-qp_i", qp, "-qp_p", qp, "-quality", amfQuality(preset)}
}

// DeviceArgs returns an empty slice; AMD AMF does not support device selection via FFmpeg.
func (e *AmfEncoder) DeviceArgs(gpuIndex int) []string {
	return []string{}
}

// IsAvailable trial-encodes a short clip to verify AMF works with the given FFmpeg binary.
func (e *AmfEncoder) IsAvailable(ffmpegPath string) bool {
	return trialEncode(ffmpegPath, "hevc_amf")
}

// ParseError inspects FFmpeg stderr for AMF-specific GPU errors and returns a human-readable message.
// Covers errors from both legacy AMF and the enhanced AMF hardware memory mapping added in FFmpeg 9.
func (e *AmfEncoder) ParseError(stderr string) (bool, string) {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "encoder creation error") {
		return true, "AMF: encoder creation failed"
	}
	// FFmpeg 9: AMF hardware memory mapping errors
	if strings.Contains(lower, "amf_dx11") ||
		strings.Contains(lower, "memory mapping") && strings.Contains(lower, "amf") {
		return true, "AMF: hardware memory mapping error (FFmpeg 9 feature — update AMD drivers)"
	}
	if strings.Contains(lower, "amf") && strings.Contains(lower, "error") {
		return true, "AMF: encoding error"
	}
	return false, ""
}

func amfQuality(preset string) string {
	switch strings.ToLower(preset) {
	case "high_quality":
		return "quality"
	case "space_saver":
		return "speed"
	default:
		return "balanced"
	}
}
