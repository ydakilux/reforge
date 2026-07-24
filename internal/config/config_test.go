package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydakilux/reforge/internal/types"
)

func TestLoadConfigReadsExistingJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test_config.json")

	want := types.Config{
		Seq: types.SeqConfig{
			ServerURL: "http://myserver:5341/",
			APIKey:    "test-key",
		},
		UsePartialHash:     false,
		MaxQueueSize:       5,
		MediaInfoPath:      "custom\\MediaInfo.exe",
		FFmpegPath:         "custom\\ffmpeg.exe",
		FFprobePath:        "custom\\ffprobe.exe",
		TempDirectory:      "C:\\Temp",
		VideoEncoder:       "libx265",
		QualityPreset:      "high_quality",
		CustomQualitySD:    18,
		CustomQuality720p:  20,
		CustomQuality1080p: 22,
		CustomQuality4K:    25,
		FileExtensions:     []string{".MP4", ".MKV"},
		LogLevel:           "DEBUG",
	}

	data, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got.Seq.ServerURL != want.Seq.ServerURL {
		t.Errorf("Seq.ServerURL = %q, want %q", got.Seq.ServerURL, want.Seq.ServerURL)
	}
	if got.Seq.APIKey != want.Seq.APIKey {
		t.Errorf("Seq.APIKey = %q, want %q", got.Seq.APIKey, want.Seq.APIKey)
	}
	if got.UsePartialHash != want.UsePartialHash {
		t.Errorf("UsePartialHash = %v, want %v", got.UsePartialHash, want.UsePartialHash)
	}
	if got.MaxQueueSize != want.MaxQueueSize {
		t.Errorf("MaxQueueSize = %d, want %d", got.MaxQueueSize, want.MaxQueueSize)
	}
	if got.VideoEncoder != want.VideoEncoder {
		t.Errorf("VideoEncoder = %q, want %q", got.VideoEncoder, want.VideoEncoder)
	}
	if got.QualityPreset != want.QualityPreset {
		t.Errorf("QualityPreset = %q, want %q", got.QualityPreset, want.QualityPreset)
	}
	if got.CustomQualitySD != want.CustomQualitySD {
		t.Errorf("CustomQualitySD = %d, want %d", got.CustomQualitySD, want.CustomQualitySD)
	}
	if got.CustomQuality4K != want.CustomQuality4K {
		t.Errorf("CustomQuality4K = %d, want %d", got.CustomQuality4K, want.CustomQuality4K)
	}
	if got.LogLevel != want.LogLevel {
		t.Errorf("LogLevel = %q, want %q", got.LogLevel, want.LogLevel)
	}
	// Migration appends missing canonical extensions, so the loaded list will be
	// at least as large as want. Verify the original entries are all present.
	for _, ext := range want.FileExtensions {
		if !containsExt(got.FileExtensions, ext) {
			t.Errorf("expected extension %q to be present, got %v", ext, got.FileExtensions)
		}
	}
}

func TestCreateDefaultConfigCreatesFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "new_config.json")

	cfg, err := CreateDefaultConfig(cfgPath)
	if err != nil {
		t.Fatalf("CreateDefaultConfig: %v", err)
	}

	// File should exist
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatal("config file was not created")
	}

	// Verify defaults
	if cfg.Seq.ServerURL != "http://localhost:5341/" {
		t.Errorf("Seq.ServerURL = %q, want %q", cfg.Seq.ServerURL, "http://localhost:5341/")
	}
	if cfg.VideoEncoder != "auto" {
		t.Errorf("VideoEncoder = %q, want %q", cfg.VideoEncoder, "auto")
	}
	if cfg.QualityPreset != "balanced" {
		t.Errorf("QualityPreset = %q, want %q", cfg.QualityPreset, "balanced")
	}
	if cfg.MaxQueueSize != 3 {
		t.Errorf("MaxQueueSize = %d, want %d", cfg.MaxQueueSize, 3)
	}
	if !cfg.UsePartialHash {
		t.Error("UsePartialHash should be true")
	}
	if cfg.LogLevel != "INFO" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "INFO")
	}
	if len(cfg.FileExtensions) != 14 {
		t.Errorf("FileExtensions len = %d, want 14", len(cfg.FileExtensions))
	}

	// Verify file is valid JSON that round-trips
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var loaded types.Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if loaded.Seq.ServerURL != cfg.Seq.ServerURL {
		t.Errorf("round-trip Seq.ServerURL mismatch")
	}
}

func TestLoadConfigCreatesDefaultWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "missing_config.json")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Should have created the file with defaults
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatal("config file was not created")
	}
	if cfg.VideoEncoder != "auto" {
		t.Errorf("VideoEncoder = %q, want %q", cfg.VideoEncoder, "auto")
	}
}

func TestAutoEncoderAccepted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "auto_config.json")

	cfg := types.Config{
		VideoEncoder:   "auto",
		FileExtensions: []string{".MP4"},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(cfgPath, data, 0644)

	_, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig with auto encoder should succeed, got: %v", err)
	}
}

func TestInvalidEncoderRejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad_config.json")

	cfg := types.Config{
		VideoEncoder:   "h264_bogus",
		FileExtensions: []string{".MP4"},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(cfgPath, data, 0644)

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("LoadConfig with invalid encoder should fail")
	}
}

func TestValidateEncoderAllValid(t *testing.T) {
	for _, enc := range ValidEncoders() {
		if err := ValidateEncoder(enc); err != nil {
			t.Errorf("ValidateEncoder(%q) should succeed, got: %v", enc, err)
		}
	}
	// Empty is also valid
	if err := ValidateEncoder(""); err != nil {
		t.Errorf("ValidateEncoder(\"\") should succeed, got: %v", err)
	}
}

func TestResolveExecutableEmptyPath(t *testing.T) {
	// Empty configPath should attempt LookPath
	result := ResolveExecutable("", "some_nonexistent_exe_12345", "C:\\fakedir")
	// LookPath for a nonexistent exe should return empty
	if result != "" {
		t.Errorf("expected empty string for nonexistent exe, got %q", result)
	}
}

func TestResolveExecutableAbsolutePath(t *testing.T) {
	// Create a temp file to act as the executable
	dir := t.TempDir()
	fakePath := filepath.Join(dir, ExeName("ffmpeg"))
	os.WriteFile(fakePath, []byte("fake"), 0755) //nolint:errcheck

	// Absolute path should be returned as-is
	result := ResolveExecutable(fakePath, ExeName("ffmpeg"), filepath.Join(dir, "irrelevant"))
	if result != fakePath {
		t.Errorf("expected %q, got %q", fakePath, result)
	}
}

func TestResolveExecutableRelativePath(t *testing.T) {
	// Create a temp "execDir" with a relative file
	dir := t.TempDir()
	subDir := filepath.Join(dir, "bin")
	os.MkdirAll(subDir, 0755) //nolint:errcheck
	fakePath := filepath.Join(subDir, ExeName("ffmpeg"))
	os.WriteFile(fakePath, []byte("fake"), 0755) //nolint:errcheck

	// Relative path should be joined with execDir
	result := ResolveExecutable(filepath.Join("bin", ExeName("ffmpeg")), ExeName("ffmpeg"), dir)
	if result != fakePath {
		t.Errorf("expected %q, got %q", fakePath, result)
	}
}

func TestConfigBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "old_config.json")

	// JSON without any GPU fields (simulates old config format)
	oldJSON := `{
	  "server_url": "http://localhost:5341/",
	  "api_key": "",
	  "use_partial_hash": true,
	  "max_queue_size": 3,
	  "video_encoder": "hevc_nvenc",
	  "quality_preset": "balanced",
	  "file_extensions": [".MP4", ".MKV"],
	  "log_level": "INFO"
	}`
	if err := os.WriteFile(cfgPath, []byte(oldJSON), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig with old format should succeed, got: %v", err)
	}

	// GPU fields should be at zero values
	if cfg.MaxEncodesPerGPU != 0 {
		t.Errorf("MaxEncodesPerGPU = %d, want 0", cfg.MaxEncodesPerGPU)
	}
	if cfg.NonInteractive {
		t.Error("NonInteractive should be false")
	}
	if cfg.GPUPreset != "" {
		t.Errorf("GPUPreset = %q, want empty", cfg.GPUPreset)
	}
	if cfg.Rebenchmark {
		t.Error("Rebenchmark should be false")
	}
}

func TestApplyGPUDefaults(t *testing.T) {
	// Zero value → should be set to default
	cfg := types.Config{}
	ApplyGPUDefaults(&cfg)
	if cfg.MaxEncodesPerGPU != 2 {
		t.Errorf("MaxEncodesPerGPU = %d, want 2", cfg.MaxEncodesPerGPU)
	}

	// Non-zero value → should be preserved
	cfg2 := types.Config{MaxEncodesPerGPU: 5}
	ApplyGPUDefaults(&cfg2)
	if cfg2.MaxEncodesPerGPU != 5 {
		t.Errorf("MaxEncodesPerGPU = %d, want 5 (preserved)", cfg2.MaxEncodesPerGPU)
	}
}

func TestRebenchmarkNotPersisted(t *testing.T) {
	cfg := types.Config{
		VideoEncoder: "hevc_nvenc",
		Rebenchmark:  true,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	jsonStr := string(data)
	if strings.Contains(jsonStr, "rebenchmark") {
		t.Errorf("JSON should not contain rebenchmark, got: %s", jsonStr)
	}
}

// TestLoadConfigWithBenchmarkSaveCacheFormat verifies that LoadConfig succeeds
// when the config file contains a benchmark_cache written by benchmark.SaveCache
// (nested structure with "results" and "version" keys).
func TestLoadConfigWithBenchmarkSaveCacheFormat(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// This is exactly the format benchmark.SaveCache writes
	configJSON := `{
	  "server_url": "http://localhost:5341/",
	  "video_encoder": "hevc_nvenc",
	  "quality_preset": "balanced",
	  "file_extensions": [".MP4"],
	  "log_level": "INFO",
	  "benchmark_cache": {
	    "results": {
	      "d885394847b264d9ec86e5995da3cf72": {
	        "encoder": "hevc_nvenc",
	        "gpu_index": 0,
	        "fps": 124.63,
	        "speed_x": 4.15,
	        "wall_clock_ms": 4814,
	        "timestamp": "2026-02-27T19:06:19.9004477+01:00",
	        "cache_key": "d885394847b264d9ec86e5995da3cf72"
	      }
	    },
	    "version": "1"
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig should succeed with benchmark.SaveCache format, got: %v", err)
	}

	// Verify other config fields survived
	if cfg.VideoEncoder != "hevc_nvenc" {
		t.Errorf("VideoEncoder = %q, want %q", cfg.VideoEncoder, "hevc_nvenc")
	}
	if cfg.QualityPreset != "balanced" {
		t.Errorf("QualityPreset = %q, want %q", cfg.QualityPreset, "balanced")
	}

	// BenchmarkCache field is no longer in Config — loading a config that still
	// contains "benchmark_cache" should silently ignore the unknown field.
}

// ── Migration tests ───────────────────────────────────────────────────────────

// TestMigrateSeqFlatFields verifies that old-style flat seq fields
// (server_url / api_key / seq_enabled) are promoted into the new cfg.Seq
// struct and persisted back to the file.
func TestMigrateSeqFlatFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "legacy_seq.json")

	legacy := `{
	  "server_url": "http://myseq:5341/",
	  "api_key": "legacy-key",
	  "seq_enabled": true,
	  "video_encoder": "libx265",
	  "quality_preset": "balanced",
	  "file_extensions": [".MP4", ".MKV"],
	  "log_level": "INFO"
	}`
	if err := os.WriteFile(cfgPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Flat fields must have been promoted.
	if cfg.Seq.ServerURL != "http://myseq:5341/" {
		t.Errorf("Seq.ServerURL = %q, want %q", cfg.Seq.ServerURL, "http://myseq:5341/")
	}
	if cfg.Seq.APIKey != "legacy-key" {
		t.Errorf("Seq.APIKey = %q, want %q", cfg.Seq.APIKey, "legacy-key")
	}
	if !cfg.Seq.Enabled {
		t.Error("Seq.Enabled should be true after migration")
	}

	// Migration must have been written back to the file.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile after migration: %v", err)
	}
	var persisted types.Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal persisted config: %v", err)
	}
	if persisted.Seq.ServerURL != "http://myseq:5341/" {
		t.Errorf("persisted Seq.ServerURL = %q", persisted.Seq.ServerURL)
	}
}

// TestMigrateNewExtensionAdded verifies that a new canonical extension (.VID)
// is appended to an existing custom list and the file is updated.
func TestMigrateNewExtensionAdded(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "no_vid.json")

	// Config without .VID
	old := `{
	  "video_encoder": "libx265",
	  "quality_preset": "balanced",
	  "file_extensions": [".MP4", ".MKV", ".AVI"],
	  "log_level": "INFO"
	}`
	if err := os.WriteFile(cfgPath, []byte(old), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// .VID must have been added.
	if !containsExt(cfg.FileExtensions, ".VID") {
		t.Errorf("expected .VID in FileExtensions after migration, got %v", cfg.FileExtensions)
	}

	// Custom entries must be preserved.
	for _, ext := range []string{".MP4", ".MKV", ".AVI"} {
		if !containsExt(cfg.FileExtensions, ext) {
			t.Errorf("expected %s to be preserved, got %v", ext, cfg.FileExtensions)
		}
	}

	// Persisted file must also contain .VID.
	data, _ := os.ReadFile(cfgPath)
	var persisted types.Config
	json.Unmarshal(data, &persisted) //nolint:errcheck
	if !containsExt(persisted.FileExtensions, ".VID") {
		t.Errorf("persisted config should contain .VID, got %v", persisted.FileExtensions)
	}
}

// TestMigrateNoChangeWhenAlreadyCurrent verifies that a fully up-to-date config
// is not needlessly rewritten.
func TestMigrateNoChangeWhenAlreadyCurrent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "current.json")

	cfg, err := CreateDefaultConfig(cfgPath)
	if err != nil {
		t.Fatalf("CreateDefaultConfig: %v", err)
	}
	_ = cfg

	stat1, _ := os.Stat(cfgPath)

	_, err = LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	stat2, _ := os.Stat(cfgPath)
	if stat1.ModTime() != stat2.ModTime() {
		t.Error("config file was rewritten even though no migration was needed")
	}
}

// TestMigrateBenchmarkCacheKeyStripped verifies that a config file that still
// contains the legacy "benchmark_cache" key (written by older versions) has
// that key removed after migration.
func TestMigrateBenchmarkCacheKeyStripped(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "legacy_bench.json")

	legacy := `{
	  "video_encoder": "hevc_nvenc",
	  "quality_preset": "balanced",
	  "file_extensions": [".MP4", ".MKV"],
	  "log_level": "INFO",
	  "benchmark_cache": {
	    "results": {},
	    "version": "1"
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// The rewritten file must not contain the benchmark_cache key.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile after migration: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, still := raw["benchmark_cache"]; still {
		t.Error("benchmark_cache key should have been removed by migration")
	}
}
