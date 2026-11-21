package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ydakilux/reforge/internal/types"
)

func newTestApp() *App {
	return &App{
		stats: types.Stats{TouchedDrives: make(map[string]bool)},
	}
}

func TestCollectSummaryData_ZeroStats(t *testing.T) {
	a := newTestApp()
	sd := a.collectSummaryData(0)

	if sd.filesAnalyzed != 0 {
		t.Errorf("filesAnalyzed = %d, want 0", sd.filesAnalyzed)
	}
	if sd.filesProcessed != 0 {
		t.Errorf("filesProcessed = %d, want 0", sd.filesProcessed)
	}
	if sd.filesImproved != 0 {
		t.Errorf("filesImproved = %d, want 0", sd.filesImproved)
	}
	if sd.filesDiscarded != 0 {
		t.Errorf("filesDiscarded = %d, want 0", sd.filesDiscarded)
	}
	if sd.filesSkipped != 0 {
		t.Errorf("filesSkipped = %d, want 0", sd.filesSkipped)
	}
	if sd.filesErrored != 0 {
		t.Errorf("filesErrored = %d, want 0", sd.filesErrored)
	}
	if sd.originalBytes != 0 {
		t.Errorf("originalBytes = %d, want 0", sd.originalBytes)
	}
	if sd.savedPct != 0 {
		t.Errorf("savedPct = %f, want 0", sd.savedPct)
	}
}

func TestCollectSummaryData_MixedStats(t *testing.T) {
	a := newTestApp()

	for i := 0; i < 10; i++ {
		a.stats.IncrFilesAnalyzed()
	}
	for i := 0; i < 3; i++ {
		a.stats.AddConverted(true, 1000, 700)
	}
	for i := 0; i < 2; i++ {
		a.stats.AddConverted(false, 500, 600)
	}
	for i := 0; i < 4; i++ {
		a.stats.IncrFilesSkipped()
	}
	a.stats.IncrFilesErrored()

	sd := a.collectSummaryData(5 * time.Minute)

	if sd.filesAnalyzed != 10 {
		t.Errorf("filesAnalyzed = %d, want 10", sd.filesAnalyzed)
	}
	if sd.filesProcessed != 5 {
		t.Errorf("filesProcessed = %d, want 5", sd.filesProcessed)
	}
	if sd.filesImproved != 3 {
		t.Errorf("filesImproved = %d, want 3", sd.filesImproved)
	}
	if sd.filesDiscarded != 2 {
		t.Errorf("filesDiscarded = %d, want 2", sd.filesDiscarded)
	}
	if sd.filesSkipped != 4 {
		t.Errorf("filesSkipped = %d, want 4", sd.filesSkipped)
	}
	if sd.filesErrored != 1 {
		t.Errorf("filesErrored = %d, want 1", sd.filesErrored)
	}

	// 3*1000 + 2*500 = 4000
	if sd.originalBytes != 4000 {
		t.Errorf("originalBytes = %d, want 4000", sd.originalBytes)
	}
	// 3*700 + 2*600 = 3300
	if sd.finalBytes != 3300 {
		t.Errorf("finalBytes = %d, want 3300", sd.finalBytes)
	}
	// 4000 - 3300 = 700
	if sd.savedBytes != 700 {
		t.Errorf("savedBytes = %d, want 700", sd.savedBytes)
	}
	// 700/4000 = 17.5%
	if sd.savedPct != 17.5 {
		t.Errorf("savedPct = %f, want 17.5", sd.savedPct)
	}
	if sd.elapsed != 5*time.Minute {
		t.Errorf("elapsed = %v, want 5m", sd.elapsed)
	}
}

func TestCollectSummaryData_OnlyImproved(t *testing.T) {
	a := newTestApp()

	a.stats.IncrFilesAnalyzed()
	a.stats.AddConverted(true, 2000, 1000)

	sd := a.collectSummaryData(0)

	if sd.filesImproved != 1 {
		t.Errorf("filesImproved = %d, want 1", sd.filesImproved)
	}
	if sd.filesDiscarded != 0 {
		t.Errorf("filesDiscarded = %d, want 0", sd.filesDiscarded)
	}
	if sd.savedPct != 50.0 {
		t.Errorf("savedPct = %f, want 50.0", sd.savedPct)
	}
	if sd.savedBytes != 1000 {
		t.Errorf("savedBytes = %d, want 1000", sd.savedBytes)
	}
}

func TestCollectSummaryData_LargeByteValues(t *testing.T) {
	a := newTestApp()

	var origPerFile int64 = 5 * 1024 * 1024 * 1024
	var finalPerFile int64 = 3 * 1024 * 1024 * 1024

	for i := 0; i < 10; i++ {
		a.stats.IncrFilesAnalyzed()
		a.stats.AddConverted(true, origPerFile, finalPerFile)
	}

	sd := a.collectSummaryData(0)

	wantOrig := origPerFile * 10
	wantFinal := finalPerFile * 10
	wantSaved := wantOrig - wantFinal

	if sd.originalBytes != wantOrig {
		t.Errorf("originalBytes = %d, want %d", sd.originalBytes, wantOrig)
	}
	if sd.finalBytes != wantFinal {
		t.Errorf("finalBytes = %d, want %d", sd.finalBytes, wantFinal)
	}
	if sd.savedBytes != wantSaved {
		t.Errorf("savedBytes = %d, want %d", sd.savedBytes, wantSaved)
	}
	if sd.savedPct != 40.0 {
		t.Errorf("savedPct = %f, want 40.0", sd.savedPct)
	}
}

func TestBuildStatsSummary_NonEmpty(t *testing.T) {
	a := newTestApp()
	lines := a.buildStatsSummary(0)

	if len(lines) == 0 {
		t.Fatal("buildStatsSummary returned no lines")
	}
}

func TestBuildStatsSummary_ContainsHeader(t *testing.T) {
	a := newTestApp()
	lines := a.buildStatsSummary(0)

	found := false
	for _, line := range lines {
		if strings.Contains(line, "VIDEO CONVERSION SUMMARY") {
			found = true
			break
		}
	}
	if !found {
		t.Error("summary does not contain 'VIDEO CONVERSION SUMMARY' header")
	}
}

func TestBuildStatsSummary_MatchesFileCounts(t *testing.T) {
	a := newTestApp()
	for i := 0; i < 7; i++ {
		a.stats.IncrFilesAnalyzed()
	}
	a.stats.AddConverted(true, 5000, 3000)
	a.stats.AddConverted(false, 4000, 5000)
	for i := 0; i < 3; i++ {
		a.stats.IncrFilesSkipped()
	}
	a.stats.IncrFilesErrored()
	a.stats.IncrFilesErrored()

	lines := a.buildStatsSummary(0)
	joined := strings.Join(lines, "\n")

	checks := map[string]string{
		"Files Analyzed":  "7",
		"Files Converted": "2",
		"Improved":        "1",
		"Discarded":       "1",
		"Files Skipped":   "3",
		"Files Errored":   "2",
	}

	for label, want := range checks {
		if !strings.Contains(joined, label) {
			t.Errorf("missing label %q in summary", label)
			continue
		}
		if !strings.Contains(joined, want) {
			t.Errorf("expected value %q for %q in summary", want, label)
		}
	}
}

func TestBuildStatsSummary_IncludesByteSizes(t *testing.T) {
	a := newTestApp()
	a.stats.AddConverted(true, 2*1024*1024, 1*1024*1024)

	lines := a.buildStatsSummary(0)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "Original Size") {
		t.Error("summary missing 'Original Size' when originalBytes > 0")
	}
	if !strings.Contains(joined, "Final Size") {
		t.Error("summary missing 'Final Size' when originalBytes > 0")
	}
	if !strings.Contains(joined, "Space Saved") {
		t.Error("summary missing 'Space Saved' when originalBytes > 0")
	}
}

func TestBuildStatsSummary_NoByteSectionWhenZero(t *testing.T) {
	a := newTestApp()
	a.stats.IncrFilesAnalyzed()
	a.stats.IncrFilesSkipped()

	lines := a.buildStatsSummary(0)
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, "Original Size") {
		t.Error("summary should not contain 'Original Size' when originalBytes == 0")
	}
}

func TestBuildStatsSummary_IncludesElapsedTime(t *testing.T) {
	a := newTestApp()
	elapsed := 2*time.Hour + 15*time.Minute + 30*time.Second

	lines := a.buildStatsSummary(elapsed)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "Total Time") {
		t.Error("summary missing 'Total Time' when elapsed > 0")
	}
	// FmtElapsed produces "2h 15m 30s"
	if !strings.Contains(joined, "2h 15m 30s") {
		t.Errorf("expected formatted elapsed '2h 15m 30s' in summary, got:\n%s", joined)
	}
}

func TestBuildStatsSummaryBox_NonEmpty(t *testing.T) {
	a := newTestApp()
	lines := a.buildStatsSummaryBox(0)

	if len(lines) == 0 {
		t.Fatal("buildStatsSummaryBox returned no lines")
	}
}

func TestBuildStatsSummaryBox_ContainsBoxDrawingChars(t *testing.T) {
	a := newTestApp()
	lines := a.buildStatsSummaryBox(0)
	joined := strings.Join(lines, "\n")

	for _, ch := range []string{"╔", "║", "╚"} {
		if !strings.Contains(joined, ch) {
			t.Errorf("box summary missing box-drawing character %q", ch)
		}
	}
}

func TestBuildStatsSummaryBox_ContainsFileCountsAndBytes(t *testing.T) {
	a := newTestApp()
	for i := 0; i < 5; i++ {
		a.stats.IncrFilesAnalyzed()
	}
	a.stats.AddConverted(true, 10*1024*1024, 6*1024*1024)
	a.stats.AddConverted(true, 8*1024*1024, 5*1024*1024)
	a.stats.IncrFilesSkipped()

	lines := a.buildStatsSummaryBox(30 * time.Second)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "Files Analyzed") {
		t.Error("box summary missing 'Files Analyzed'")
	}
	if !strings.Contains(joined, "Original Size") {
		t.Error("box summary missing 'Original Size' when bytes > 0")
	}
	if !strings.Contains(joined, "Total Time") {
		t.Error("box summary missing 'Total Time' when elapsed > 0")
	}
}

func TestClampParallelJobs(t *testing.T) {
	for in, want := range map[int]int{-3: 1, 0: 1, 1: 1, 5: 5, MaxParallelJobsCap: MaxParallelJobsCap, MaxParallelJobsCap + 7: MaxParallelJobsCap, 1000: MaxParallelJobsCap} {
		if got := clampParallelJobs(in); got != want {
			t.Errorf("clampParallelJobs(%d) = %d, want %d", in, got, want)
		}
	}
}
