package types

import (
	"sync"
)

// SeqConfig holds all settings for optional Seq structured-log forwarding.
type SeqConfig struct {
	Enabled   bool   `json:"enabled"`
	ServerURL string `json:"server_url"`
	APIKey    string `json:"api_key"`
}

// Config holds application configuration
type Config struct {
	Seq                SeqConfig `json:"seq"`
	UsePartialHash     bool      `json:"use_partial_hash"`
	MaxQueueSize       int       `json:"max_queue_size"`
	MediaInfoPath      string    `json:"mediainfo_path"`
	FFmpegPath         string    `json:"ffmpeg_path"`
	FFprobePath        string    `json:"ffprobe_path"`
	TempDirectory      string    `json:"temp_directory"`
	VideoEncoder       string    `json:"video_encoder"`
	QualityPreset      string    `json:"quality_preset"`
	CustomQualitySD    int       `json:"custom_quality_sd,omitempty"`
	CustomQuality720p  int       `json:"custom_quality_720p,omitempty"`
	CustomQuality1080p int       `json:"custom_quality_1080p,omitempty"`
	CustomQuality4K    int       `json:"custom_quality_4k,omitempty"`
	FileExtensions     []string  `json:"file_extensions"`
	LogLevel           string    `json:"log_level"`
	// GPU-related fields
	MaxEncodesPerGPU int    `json:"max_encodes_per_gpu,omitempty"`
	NonInteractive   bool   `json:"-"` // runtime-only, NOT persisted
	GPUPreset        string `json:"gpu_preset,omitempty"`
	Rebenchmark      bool   `json:"-"` // runtime-only, NOT persisted
	// Parallelism
	MaxParallelJobs int `json:"max_parallel_jobs,omitempty"`
}

// Record represents a cache entry
type Record struct {
	OriginalSize  int64  `json:"original_size"`
	ConvertedSize int64  `json:"converted_size,omitempty"`
	Output        string `json:"output,omitempty"`
	Note          string `json:"note,omitempty"`
	Error         string `json:"error,omitempty"`

	SourceCodec            string  `json:"source_codec,omitempty"`
	SourceContainer        string  `json:"source_container,omitempty"`
	SourcePath             string  `json:"source_path,omitempty"`
	Width                  int     `json:"width,omitempty"`
	Height                 int     `json:"height,omitempty"`
	DurationSecs           float64 `json:"duration_secs,omitempty"`
	ConvertedAt            string  `json:"converted_at,omitempty"`
	ConversionDurationSecs float64 `json:"conversion_duration_secs,omitempty"`

	// RunID associates this record with a row in the runs table. Zero means
	// "not part of a run" (e.g. discovery prescan or legacy data prior to
	// the runs feature). When non-zero it is persisted as conversions.run_id.
	RunID int64 `json:"run_id,omitempty"`
}

// Job represents a conversion job
type Job struct {
	FilePath        string
	BaseDir         string // User-provided input directory (dropped folder)
	DriveRoot       string
	FileHash        string
	OriginalSize    int64
	Width           int
	Height          int
	Format          string
	CodecID         string
	DurationSeconds float64
	FileNumber      int        // Current file number
	TotalFiles      int        // Total files to process
	FolderNumber    int        // Current folder number
	TotalFolders    int        // Total folders
	GPUIndex        int        // GPU index for multi-GPU encoding
	VideoInfo       *VideoInfo // Full stream info for smart codec decisions (may be nil)
}

// Stats tracks conversion statistics
type Stats struct {
	Mu             sync.Mutex
	FilesAnalyzed  int
	FilesProcessed int
	FilesImproved  int
	FilesDiscarded int
	FilesSkipped   int
	FilesErrored   int
	OriginalBytes  int64
	FinalBytes     int64
	TouchedDrives  map[string]bool
}

// IncrFilesAnalyzed safely increments FilesAnalyzed by 1.
func (s *Stats) IncrFilesAnalyzed() {
	s.Mu.Lock()
	s.FilesAnalyzed++
	s.Mu.Unlock()
}

// IncrFilesSkipped safely increments FilesSkipped by 1.
func (s *Stats) IncrFilesSkipped() {
	s.Mu.Lock()
	s.FilesSkipped++
	s.Mu.Unlock()
}

// IncrFilesErrored safely increments FilesErrored by 1.
func (s *Stats) IncrFilesErrored() {
	s.Mu.Lock()
	s.FilesErrored++
	s.Mu.Unlock()
}

// AddConverted records a completed conversion outcome.
// improved=true means the output was smaller than the input (kept);
// false means it was discarded.
func (s *Stats) AddConverted(improved bool, origBytes, finalBytes int64) {
	s.Mu.Lock()
	s.FilesProcessed++
	if improved {
		s.FilesImproved++
	} else {
		s.FilesDiscarded++
	}
	s.OriginalBytes += origBytes
	s.FinalBytes += finalBytes
	s.Mu.Unlock()
}

// AddTouchedDrive records that a drive root has been written to.
func (s *Stats) AddTouchedDrive(driveRoot string) {
	s.Mu.Lock()
	s.TouchedDrives[driveRoot] = true
	s.Mu.Unlock()
}

// AudioStream holds information about a single audio stream in a video file.
type AudioStream struct {
	CodecName string
	Channels  int
}

// SubtitleStream holds information about a single subtitle stream in a video file.
type SubtitleStream struct {
	CodecName string
}

// ColorInfo holds HDR/colour-space metadata from the primary video stream.
type ColorInfo struct {
	ColorPrimaries string // e.g. "bt2020", "bt709"
	ColorTransfer  string // e.g. "smpte2084", "arib-std-b67", "bt709"
	ColorSpace     string // e.g. "bt2020nc", "bt709"
}

// VideoInfo holds media information about a video file
type VideoInfo struct {
	Format          string
	Width           int
	Height          int
	CodecID         string
	Color           ColorInfo
	AudioStreams    []AudioStream
	SubtitleStreams []SubtitleStream
}
