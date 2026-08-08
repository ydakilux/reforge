package encoder

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestNvencImplementsEncoder(t *testing.T) {
	var _ Encoder = (*NvencEncoder)(nil)
}

func TestNvencName(t *testing.T) {
	enc := NewNvencEncoder()
	if got := enc.Name(); got != "hevc_nvenc" {
		t.Errorf("Name() = %q, want %q", got, "hevc_nvenc")
	}
}

func TestNvencQualityArgs(t *testing.T) {
	enc := NewNvencEncoder()

	tests := []struct {
		name   string
		preset string
		width  int
		want   []string
	}{

		{"balanced_sd", "balanced", 800, []string{"-cq", "24", "-preset", "p5"}},
		{"balanced_720p", "balanced", 1280, []string{"-cq", "26", "-preset", "p5"}},
		{"balanced_1080p", "balanced", 1920, []string{"-cq", "28", "-preset", "p5"}},
		{"balanced_4k", "balanced", 3840, []string{"-cq", "30", "-preset", "p5"}},

		{"hq_sd", "high_quality", 1024, []string{"-cq", "20", "-preset", "p7"}},
		{"hq_720p", "high_quality", 1280, []string{"-cq", "22", "-preset", "p7"}},
		{"hq_1080p", "high_quality", 1920, []string{"-cq", "24", "-preset", "p7"}},
		{"hq_4k", "high_quality", 3840, []string{"-cq", "26", "-preset", "p7"}},

		{"ss_sd", "space_saver", 640, []string{"-cq", "28", "-preset", "p4"}},
		{"ss_720p", "space_saver", 1280, []string{"-cq", "30", "-preset", "p4"}},
		{"ss_1080p", "space_saver", 1920, []string{"-cq", "32", "-preset", "p4"}},
		{"ss_4k", "space_saver", 2560, []string{"-cq", "35", "-preset", "p4"}},

		{"unknown_1080p", "custom", 1920, []string{"-cq", "28", "-preset", "p5"}},
		{"empty_1080p", "", 1920, []string{"-cq", "28", "-preset", "p5"}},

		{"uppercase_balanced", "BALANCED", 1920, []string{"-cq", "28", "-preset", "p5"}},
		{"mixedcase_hq", "High_Quality", 1920, []string{"-cq", "24", "-preset", "p7"}},
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

func TestNvencDeviceArgs(t *testing.T) {
	enc := NewNvencEncoder()

	tests := []struct {
		name     string
		gpuIndex int
		want     []string
	}{
		{"gpu_0", 0, []string{"-gpu", "0"}},
		{"gpu_1", 1, []string{"-gpu", "1"}},
		{"gpu_2", 2, []string{"-gpu", "2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := enc.DeviceArgs(tt.gpuIndex)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DeviceArgs(%d) = %v, want %v", tt.gpuIndex, got, tt.want)
			}
		})
	}
}

func TestNvencParseError(t *testing.T) {
	enc := NewNvencEncoder()

	tests := []struct {
		name      string
		stderr    string
		wantIsGPU bool
		wantMsg   string
	}{
		{
			"no_capable_devices",
			"Error: No capable devices found",
			true,
			"NVENC: no capable GPU devices",
		},
		{
			"session_failed",
			"[hevc_nvenc] OpenEncodeSessionEx failed: out of memory",
			true,
			"NVENC: session limit or memory error",
		},
		{
			"init_failed",
			"[hevc_nvenc] InitializeEncoder failed",
			true,
			"NVENC: encoder initialization failed",
		},
		{
			"driver_api_version_mismatch",
			"[hevc_nvenc @ 0x...] Driver does not support the required nvenc API version. Required: 13.0 Found: 12.2",
			true,
			"NVENC: driver too old — update NVIDIA driver to ≥ 610 to use GPU encoding with FFmpeg 9",
		},
		{
			"minimum_driver_message",
			"[hevc_nvenc @ 0x...] The minimum required Nvidia driver for nvenc is 570.0 or newer",
			true,
			"NVENC: driver too old — update NVIDIA driver to ≥ 610 to use GPU encoding with FFmpeg 9",
		},
		{
			"error_while_opening_encoder",
			"[vost#0:0] [enc:hevc_nvenc] Error while opening encoder – maybe incorrect parameters such as bit_rate, rate, width or height",
			true,
			"NVENC: driver too old — update NVIDIA driver to ≥ 610 to use GPU encoding with FFmpeg 9",
		},
		// FFmpeg 9: NVENC SDK version mismatch (SDK < 11.1 not supported)
		{
			"ffmpeg9_nvenc_sdk_version",
			"[hevc_nvenc @ 0x...] nvenc sdk version 10.0 is not supported",
			true,
			"NVENC: driver too old — update NVIDIA driver to ≥ 610 to use GPU encoding with FFmpeg 9",
		},
		// Real FFmpeg 9.0 error: NVENC API 13.1 required, driver >= 610.00 needed
		{
			"ffmpeg9_api13_mismatch",
			"[hevc_nvenc @ 0000018943135f00] Driver does not support the required nvenc API version. Required: 13.1 Found: 13.0\n[hevc_nvenc @ 0000018943135f00] The minimum required Nvidia driver for nvenc is 610.00 or newer",
			true,
			"NVENC: driver too old — update NVIDIA driver to ≥ 610 to use GPU encoding with FFmpeg 9",
		},
		{
			"normal_output",
			"frame= 100 fps=50 q=28.0 size= 1024kB time=00:00:04.00",
			false,
			"",
		},
		{
			"empty_stderr",
			"",
			false,
			"",
		},
		{
			"unrelated_error",
			"Error opening input file: No such file or directory",
			false,
			"",
		},
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

func TestNvencIsAvailable(t *testing.T) {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found in PATH, skipping availability test")
	}

	enc := NewNvencEncoder()
	_ = enc.IsAvailable("ffmpeg")

}

func TestNvencIsAvailableInvalidPath(t *testing.T) {
	enc := NewNvencEncoder()
	if enc.IsAvailable("/nonexistent/ffmpeg") {
		t.Error("IsAvailable(/nonexistent/ffmpeg) = true, want false")
	}
}
