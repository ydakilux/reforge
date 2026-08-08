package encoder

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestAmfName(t *testing.T) {
	enc := NewAmfEncoder()
	if got := enc.Name(); got != "hevc_amf" {
		t.Errorf("Name() = %q, want %q", got, "hevc_amf")
	}
}

func TestAmfQualityArgs(t *testing.T) {
	enc := NewAmfEncoder()

	tests := []struct {
		name   string
		preset string
		width  int
		want   []string
	}{
		// balanced preset (default)
		{"balanced_sd", "balanced", 800, []string{"-rc", "cqp", "-qp_i", "20", "-qp_p", "20", "-quality", "balanced"}},
		{"balanced_720p", "balanced", 1280, []string{"-rc", "cqp", "-qp_i", "22", "-qp_p", "22", "-quality", "balanced"}},
		{"balanced_1080p", "balanced", 1920, []string{"-rc", "cqp", "-qp_i", "24", "-qp_p", "24", "-quality", "balanced"}},
		{"balanced_4k", "balanced", 3840, []string{"-rc", "cqp", "-qp_i", "27", "-qp_p", "27", "-quality", "balanced"}},

		// high_quality preset
		{"hq_sd", "high_quality", 1024, []string{"-rc", "cqp", "-qp_i", "16", "-qp_p", "16", "-quality", "quality"}},
		{"hq_720p", "high_quality", 1280, []string{"-rc", "cqp", "-qp_i", "18", "-qp_p", "18", "-quality", "quality"}},
		{"hq_1080p", "high_quality", 1920, []string{"-rc", "cqp", "-qp_i", "20", "-qp_p", "20", "-quality", "quality"}},
		{"hq_4k", "high_quality", 3840, []string{"-rc", "cqp", "-qp_i", "22", "-qp_p", "22", "-quality", "quality"}},

		// space_saver preset
		{"ss_sd", "space_saver", 640, []string{"-rc", "cqp", "-qp_i", "24", "-qp_p", "24", "-quality", "speed"}},
		{"ss_720p", "space_saver", 1280, []string{"-rc", "cqp", "-qp_i", "26", "-qp_p", "26", "-quality", "speed"}},
		{"ss_1080p", "space_saver", 1920, []string{"-rc", "cqp", "-qp_i", "28", "-qp_p", "28", "-quality", "speed"}},
		{"ss_4k", "space_saver", 2560, []string{"-rc", "cqp", "-qp_i", "31", "-qp_p", "31", "-quality", "speed"}},

		// unknown preset falls back to balanced
		{"unknown_1080p", "custom", 1920, []string{"-rc", "cqp", "-qp_i", "24", "-qp_p", "24", "-quality", "balanced"}},
		{"empty_1080p", "", 1920, []string{"-rc", "cqp", "-qp_i", "24", "-qp_p", "24", "-quality", "balanced"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := enc.QualityArgs(tt.preset, tt.width)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("QualityArgs(%q, %d) = %v, want %v", tt.preset, tt.width, got, tt.want)
			}
		})
	}
}

func TestAmfDeviceArgs(t *testing.T) {
	enc := NewAmfEncoder()
	got0 := enc.DeviceArgs(0)
	if len(got0) != 0 {
		t.Errorf("DeviceArgs(0) = %v, want empty slice", got0)
	}
	got1 := enc.DeviceArgs(1)
	if len(got1) != 0 {
		t.Errorf("DeviceArgs(1) = %v, want empty slice", got1)
	}
}

func TestAmfParseError(t *testing.T) {
	enc := NewAmfEncoder()

	tests := []struct {
		name      string
		stderr    string
		wantIsGPU bool
		wantMsg   string
	}{
		{"encoder_creation", "Encoder creation error", true, "AMF: encoder creation failed"},
		{"amf_error", "amf error in encoding", true, "AMF: encoding error"},
		{"amf_error_mixed_case", "AMF Error occurred", true, "AMF: encoding error"},
		// FFmpeg 9: AMF hardware memory mapping errors
		{"ffmpeg9_amf_dx11_error", "[hevc_amf @ 0x...] amf_dx11: device not found", true, "AMF: hardware memory mapping error (FFmpeg 9 feature — update AMD drivers)"},
		{"ffmpeg9_amf_memory_mapping", "[hevc_amf @ 0x...] AMF memory mapping failed: device lost", true, "AMF: hardware memory mapping error (FFmpeg 9 feature — update AMD drivers)"},
		{"normal_output", "normal output no issues", false, ""},
		{"empty_stderr", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIsGPU, gotMsg := enc.ParseError(tt.stderr)
			if gotIsGPU != tt.wantIsGPU {
				t.Errorf("ParseError(%q) isGPUError = %v, want %v", tt.stderr, gotIsGPU, tt.wantIsGPU)
			}
			if gotMsg != tt.wantMsg {
				t.Errorf("ParseError(%q) msg = %q, want %q", tt.stderr, gotMsg, tt.wantMsg)
			}
		})
	}
}

func TestAmfIsAvailable(t *testing.T) {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not in PATH, skipping availability test")
	}
	enc := NewAmfEncoder()
	// We just call it — result depends on whether AMD GPU is present.
	// The test verifies the method doesn't panic.
	_ = enc.IsAvailable("ffmpeg")
}

func TestAmfImplementsEncoder(t *testing.T) {
	var _ Encoder = (*AmfEncoder)(nil)
}
