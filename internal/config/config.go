// Package config handles loading, creating, migrating, and validating the JSON configuration file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ydakilux/reforge/internal/types"
)

// ValidEncoders returns the list of accepted encoder names.
// Returned as a fresh slice so callers cannot mutate the canonical list.
func ValidEncoders() []string {
	return []string{"auto", "hevc_nvenc", "hevc_amf", "hevc_qsv", "libx265"}
}

// ExeName appends ".exe" to name on Windows, returns name unchanged elsewhere.
func ExeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// legacyConfig captures pre-SeqConfig flat fields so LoadConfig can migrate
// old JSON files transparently.
type legacyConfig struct {
	ServerURL  string `json:"server_url"`
	APIKey     string `json:"api_key"`
	SeqEnabled bool   `json:"seq_enabled"`
}

// LoadConfig reads the JSON configuration from path, applying any pending
// migrations (legacy Seq fields, missing extensions, stale benchmark_cache).
// If path does not exist, a default config is created and returned.
func LoadConfig(path string) (types.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return CreateDefaultConfig(path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return types.Config{}, err
	}
	var cfg types.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return types.Config{}, err
	}
	if cfg.VideoEncoder == "" {
		cfg.VideoEncoder = "auto"
	}
	if err := ValidateEncoder(cfg.VideoEncoder); err != nil {
		return types.Config{}, err
	}

	migrated := migrate(&cfg, data)
	if migrated {
		if err := writeConfig(path, cfg); err != nil {
			// Non-fatal: return the migrated config even if we can't persist it.
			fmt.Fprintf(os.Stderr, "WARNING: could not persist config migration: %v\n", err)
		}
	}

	return cfg, nil
}

// migrate applies any pending in-place migrations to cfg, using the raw JSON
// bytes to detect which legacy fields were present. Returns true if anything
// was changed.
func migrate(cfg *types.Config, raw []byte) bool {
	changed := false

	// ── Migration 1: flat Seq fields → cfg.Seq struct ───────────────────────
	// If the new "seq" object is at its zero value but the old flat keys exist
	// in the JSON, promote them.
	if cfg.Seq == (types.SeqConfig{}) {
		var leg legacyConfig
		if err := json.Unmarshal(raw, &leg); err == nil {
			if leg.ServerURL != "" || leg.APIKey != "" || leg.SeqEnabled {
				cfg.Seq = types.SeqConfig{
					Enabled:   leg.SeqEnabled,
					ServerURL: leg.ServerURL,
					APIKey:    leg.APIKey,
				}
				changed = true
			}
		}
	}

	// ── Migration 2: ensure known extensions are present ────────────────────
	// Add any extension from the canonical default list that is missing from
	// the user's list, preserving their custom entries and order.
	for _, ext := range defaultExtensions() {
		if !containsExt(cfg.FileExtensions, ext) {
			cfg.FileExtensions = append(cfg.FileExtensions, ext)
			changed = true
		}
	}

	// ── Migration 3: strip legacy benchmark_cache key from config file ───────
	// Older versions wrote benchmark results directly into the config JSON.
	// The benchmark subsystem now uses a separate benchmark_cache.json file.
	// Detect the stale key and trigger a rewrite so it disappears.
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err == nil {
		if _, hasBench := rawMap["benchmark_cache"]; hasBench {
			changed = true // rewrite will serialise cfg which has no such field
		}
	}

	return changed
}

// defaultExtensions returns the canonical set of extensions every config
// should have. New entries added here are automatically migrated into old
// config files on next load.
func defaultExtensions() []string {
	return []string{".MOV", ".AVI", ".MKV", ".MP4", ".WMV", ".M4V", ".FLV", ".F4V", ".MPG", ".ASF", ".TS", ".M2TS", ".VID", ".WEBM"}
}

// containsExt reports whether ext (case-insensitive) is in list.
func containsExt(list []string, ext string) bool {
	upper := strings.ToUpper(ext)
	for _, e := range list {
		if strings.ToUpper(e) == upper {
			return true
		}
	}
	return false
}

// CreateDefaultConfig writes a new config file at path with sensible defaults and returns the resulting Config.
func CreateDefaultConfig(path string) (types.Config, error) {
	cfg := types.Config{
		Seq: types.SeqConfig{
			Enabled:   false,
			ServerURL: "http://localhost:5341/",
			APIKey:    "",
		},
		UsePartialHash:     true,
		MaxQueueSize:       3,
		MaxParallelJobs:    1,
		MediaInfoPath:      defaultMediaInfoPath(),
		FFmpegPath:         defaultFFmpegPath(),
		FFprobePath:        "",
		TempDirectory:      "",
		VideoEncoder:       "auto",
		QualityPreset:      "balanced",
		CustomQualitySD:    0,
		CustomQuality720p:  0,
		CustomQuality1080p: 0,
		CustomQuality4K:    0,
		FileExtensions:     defaultExtensions(),
		LogLevel:           "INFO",
	}
	if err := writeConfig(path, cfg); err != nil {
		return types.Config{}, err
	}
	return cfg, nil
}

// defaultMediaInfoPath returns the platform-appropriate default MediaInfo path.
// Returns an empty string on non-Windows so the tool falls back to PATH lookup.
func defaultMediaInfoPath() string {
	if runtime.GOOS == "windows" {
		return `MediaInfo_CLI_24.04_Windows_x64\MediaInfo.exe`
	}
	return "" // rely on PATH: mediainfo
}

// defaultFFmpegPath returns the platform-appropriate default ffmpeg path.
// Returns an empty string on non-Windows so the tool falls back to PATH lookup.
func defaultFFmpegPath() string {
	if runtime.GOOS == "windows" {
		return `ffmpeg\bin\ffmpeg.exe`
	}
	return "" // rely on PATH: ffmpeg
}

// SaveConfig serialises cfg to path atomically (write to .tmp then rename).
func SaveConfig(path string, cfg types.Config) error {
	return writeConfig(path, cfg)
}

// writeConfig serialises cfg to path atomically (write to .tmp then rename).
func writeConfig(path string, cfg types.Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ValidateEncoder returns an error if encoder is not in the set returned by ValidEncoders.
func ValidateEncoder(encoder string) error {
	if encoder == "" {
		return nil
	}
	for _, valid := range ValidEncoders() {
		if encoder == valid {
			return nil
		}
	}
	return fmt.Errorf("invalid video encoder %q; valid options: %s", encoder, strings.Join(ValidEncoders(), ", "))
}

// ResolveExecutable resolves the path to an executable. It first checks the
// config-supplied path (absolute or relative to execDir); if not found or empty,
// it falls back to exec.LookPath.
func ResolveExecutable(configPath, exeName, execDir string) string {
	if configPath == "" {
		exe, _ := exec.LookPath(exeName)
		return exe
	}

	var path string
	if filepath.IsAbs(configPath) {
		path = configPath
	} else {
		path = filepath.Join(execDir, configPath)
	}

	if _, err := os.Stat(path); err == nil {
		return path
	}

	exe, _ := exec.LookPath(exeName)
	return exe
}

// ApplyGPUDefaults sets default values for GPU-related config fields.
// Only sets defaults when fields are at their zero value.
func ApplyGPUDefaults(cfg *types.Config) {
	if cfg.MaxEncodesPerGPU == 0 {
		cfg.MaxEncodesPerGPU = 2
	}
	if cfg.MaxParallelJobs == 0 {
		cfg.MaxParallelJobs = 1
	}
}
