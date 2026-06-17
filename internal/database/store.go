package database

import (
	"context"

	"github.com/ydakilux/reforge/internal/types"
)

// Stats holds aggregate conversion statistics.
type Stats struct {
	TotalFiles     int
	TotalOriginal  int64
	TotalConverted int64
	ErrorCount     int
	NotBeneficial  int
	AlreadyHEVC    int
	SuccessCount   int
}

// ErrorRecord represents a failed conversion.
type ErrorRecord struct {
	FileHash     string
	DriveRoot    string
	SourcePath   string
	OriginalSize int64
	Error        string
	ConvertedAt  string
}

// NotBeneficialRecord represents a conversion where the output was larger.
type NotBeneficialRecord struct {
	FileHash      string
	DriveRoot     string
	SourcePath    string
	OriginalSize  int64
	ConvertedSize int64
}

// RecentRecord represents a recent conversion with full details.
type RecentRecord struct {
	FileHash      string
	DriveRoot     string
	SourcePath    string
	OriginalSize  int64
	ConvertedSize int64
	OutputPath    string
	Note          string
	Error         string
	SourceCodec   string
	ConvertedAt   string
}

// FormatStat represents conversion stats for a source format.
type FormatStat struct {
	SourceCodec     string
	SourceContainer string
	Count           int
	TotalOriginal   int64
	TotalConverted  int64
}

// SpaceSavedResult holds space saved aggregation.
type SpaceSavedResult struct {
	Period     string
	FileCount  int
	BytesSaved int64
}

// TimelinePoint represents a single day's conversion statistics.
type TimelinePoint struct {
	Date           string
	Count          int
	TotalOriginal  int64
	TotalConverted int64
	BytesSaved     int64
	DriveRoot      string
}

// RunInfo is the parameters used to create a new run row.
type RunInfo struct {
	StartedAt     string   // RFC3339 UTC
	SourcePaths   []string // user-supplied input paths
	OutputDrive   string   // "" means same-drive
	Encoder       string
	QualityPreset string
	ParallelJobs  int
	Note          string // optional tag (e.g. "legacy")
}

// RunStats is the running counters updated when a run completes.
type RunStats struct {
	EndedAt        string // RFC3339 UTC
	FileCount      int
	OriginalBytes  int64
	ConvertedBytes int64
	ErrorCount     int
	NotBeneficial  int
}

// RunSummary is a row in the runs list shown to the user.
type RunSummary struct {
	ID             int64
	StartedAt      string
	EndedAt        string
	SourcePaths    []string
	OutputDrive    string
	Encoder        string
	QualityPreset  string
	ParallelJobs   int
	FileCount      int
	OriginalBytes  int64
	ConvertedBytes int64
	ErrorCount     int
	NotBeneficial  int
	Note           string

	// Per-run breakdown computed from the conversions table. These are
	// derived counters so the UI can distinguish skip-only sessions
	// (everything was already HEVC) from real conversion runs without
	// re-reading every file row.
	SkippedAlreadyHEVC int // conversions.note = 'already_hevc'
	ConvertedKept      int // successful conversion that was kept (note IS NULL/empty, error IS NULL/empty)
	NotBeneficialFiles int // conversions.note = 'not_beneficial'
	ErroredFiles       int // conversions.error IS NOT NULL AND error <> ''
	AttachedTotal      int // total rows whose run_id = this run
}

// IsSkipOnly reports whether the run did no real conversion work — every
// attached row was an "already HEVC" skip. Useful for rendering the Recent
// runs list with a clear marker instead of a misleading "Saved: 0 B".
func (r RunSummary) IsSkipOnly() bool {
	if r.AttachedTotal == 0 {
		return false
	}
	return r.SkippedAlreadyHEVC == r.AttachedTotal
}

// RunFileRecord is one conversion record listed under a run.
type RunFileRecord struct {
	FileHash      string
	DriveRoot     string
	SourcePath    string
	OutputPath    string
	OriginalSize  int64
	ConvertedSize int64
	Note          string
	Error         string
	SourceCodec   string
	ConvertedAt   string
}

// Store defines the interface for conversion record storage.
type Store interface {
	// GetRecord retrieves a conversion record by drive root and file hash.
	// Returns (nil, nil) if the record does not exist.
	GetRecord(ctx context.Context, driveRoot, fileHash string) (*types.Record, error)

	// UpdateRecord creates or updates a conversion record.
	UpdateRecord(ctx context.Context, driveRoot, fileHash string, rec types.Record) error

	// Close closes the store and releases resources.
	Close() error

	// GetStats returns aggregate statistics, optionally filtered by drive root.
	// Pass driveRoot="" for all drives.
	GetStats(ctx context.Context, driveRoot string) (*Stats, error)

	// GetErrors returns records that failed conversion.
	GetErrors(ctx context.Context, driveRoot, pathFilter string) ([]ErrorRecord, error)

	// GetNotBeneficial returns records where conversion was not beneficial.
	GetNotBeneficial(ctx context.Context, driveRoot string) ([]NotBeneficialRecord, error)

	// GetRecent returns the most recent conversion records.
	GetRecent(ctx context.Context, limit int) ([]RecentRecord, error)

	// GetFormatBreakdown returns conversion statistics grouped by source format.
	GetFormatBreakdown(ctx context.Context, driveRoot string) ([]FormatStat, error)

	// GetSpaceSaved returns total space saved for a given time period.
	// period: "week" (7 days), "month" (30 days), "total" (all time)
	GetSpaceSaved(ctx context.Context, period string) (*SpaceSavedResult, error)

	GetConversionTimeline(ctx context.Context) ([]TimelinePoint, error)

	GetDriveRoots(ctx context.Context) ([]string, error)

	// CreateRun inserts a new run row and returns its id.
	CreateRun(ctx context.Context, info RunInfo) (int64, error)

	// FinalizeRun updates the run row's ended_at and aggregate counters.
	FinalizeRun(ctx context.Context, runID int64, stats RunStats) error

	// GetRuns returns runs ordered by started_at descending.
	GetRuns(ctx context.Context, limit int) ([]RunSummary, error)

	// GetRunFiles returns the conversion records attached to a given run.
	GetRunFiles(ctx context.Context, runID int64) ([]RunFileRecord, error)
}
