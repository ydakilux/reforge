package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/sirupsen/logrus"

	"github.com/ydakilux/reforge/internal/dashboard"
	"github.com/ydakilux/reforge/internal/database"
	"github.com/ydakilux/reforge/internal/fileutil"
)

func defaultDBPath() string {
	exePath, err := os.Executable()
	if err != nil {
		return "conversions.db"
	}
	return filepath.Join(filepath.Dir(exePath), "conversions.db")
}

func subcommandLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

func openStore(dbPath string) (database.Store, func()) {
	store, err := database.NewSQLiteStore(dbPath, subcommandLogger())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	return store, func() { store.Close() }
}

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
		tb = gb * 1024
	)
	switch {
	case b >= tb:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(tb))
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// ── Renderers ────────────────────────────────────────────────────────────────
// Each Render* writes a human-readable report to w. They are reused both by
// the CLI subcommands and by the interactive launcher menu.

// RenderStats writes the aggregate statistics report.
func RenderStats(ctx context.Context, w io.Writer, store database.Store, drive string) error {
	st, err := store.GetStats(ctx, drive)
	if err != nil {
		return err
	}

	savedBytes := st.TotalOriginal - st.TotalConverted
	savedPct := 0.0
	if st.TotalOriginal > 0 {
		savedPct = float64(savedBytes) / float64(st.TotalOriginal) * 100
	}

	fmt.Fprintln(w, "Conversion Statistics")
	fmt.Fprintln(w, "─────────────────────────────────")
	fmt.Fprintf(w, "Total files:      %d\n", st.TotalFiles)
	fmt.Fprintf(w, "  Successful:     %d\n", st.SuccessCount)
	fmt.Fprintf(w, "  Errors:         %d\n", st.ErrorCount)
	fmt.Fprintf(w, "  Not beneficial: %d\n", st.NotBeneficial)
	fmt.Fprintf(w, "  Already HEVC:   %d\n", st.AlreadyHEVC)
	fmt.Fprintln(w, "─────────────────────────────────")
	fmt.Fprintf(w, "Total original:   %s\n", formatBytes(st.TotalOriginal))
	fmt.Fprintf(w, "Total converted:  %s\n", formatBytes(st.TotalConverted))
	fmt.Fprintf(w, "Space saved:      %s (%.1f%%)\n", formatBytes(savedBytes), savedPct)
	return nil
}

// RenderErrors writes the list of failed conversions.
func RenderErrors(ctx context.Context, w io.Writer, store database.Store, drive, pathFilter string) error {
	records, err := store.GetErrors(ctx, drive, pathFilter)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(w, "No errors found.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Hash\tDrive\tSource Path\tSize\tError\tDate\n")
	fmt.Fprintf(tw, "────\t─────\t───────────\t────\t─────\t────\n")
	for _, r := range records {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.FileHash, r.DriveRoot, r.SourcePath,
			formatBytes(r.OriginalSize), r.Error, r.ConvertedAt)
	}
	return tw.Flush()
}

// RenderRecent writes the most recent conversion records, preceded by a header
// summarising the unique source/destination drive roots and aggregate space
// totals across the displayed records.
func RenderRecent(ctx context.Context, w io.Writer, store database.Store, limit int) error {
	records, err := store.GetRecent(ctx, limit)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(w, "No data found.")
		return nil
	}

	// ── Header: aggregate sources, destinations, and space totals ──────────
	sourceRoots := make(map[string]bool)
	destRoots := make(map[string]bool)
	var (
		totalOriginal  int64
		totalConverted int64
		convertedCount int // records with a non-zero converted size (i.e. real conversions)
	)
	for _, r := range records {
		if r.SourcePath != "" {
			sourceRoots[fileutil.GetDriveRoot(r.SourcePath)] = true
		} else if r.DriveRoot != "" {
			sourceRoots[r.DriveRoot] = true
		}
		if r.OutputPath != "" {
			destRoots[fileutil.GetDriveRoot(r.OutputPath)] = true
		}
		totalOriginal += r.OriginalSize
		totalConverted += r.ConvertedSize
		if r.ConvertedSize > 0 {
			convertedCount++
		}
	}

	fmt.Fprintf(w, "Recent conversions (%d)\n", len(records))
	fmt.Fprintln(w, "─────────────────────────────────────────────────────────────")

	writeRootList := func(label string, set map[string]bool) {
		if len(set) == 0 {
			return
		}
		roots := make([]string, 0, len(set))
		for r := range set {
			roots = append(roots, r)
		}
		sort.Strings(roots)
		if len(roots) == 1 {
			fmt.Fprintf(w, "%-14s %s\n", label+":", roots[0])
			return
		}
		fmt.Fprintf(w, "%s (%d):\n", label, len(roots))
		for _, r := range roots {
			fmt.Fprintf(w, "  • %s\n", r)
		}
	}
	writeRootList("Source drive", sourceRoots)
	writeRootList("Destination", destRoots)

	saved := totalOriginal - totalConverted
	savedPct := 0.0
	if totalOriginal > 0 {
		savedPct = float64(saved) / float64(totalOriginal) * 100
	}
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Totals across %d record(s), %d with a converted output:\n", len(records), convertedCount)
	fmt.Fprintf(w, "  Original:    %s\n", formatBytes(totalOriginal))
	fmt.Fprintf(w, "  Converted:   %s\n", formatBytes(totalConverted))
	if totalOriginal > 0 {
		fmt.Fprintf(w, "  Space saved: %s  (%.1f%% reduction)\n", formatBytes(saved), savedPct)
	}
	fmt.Fprintln(w, "─────────────────────────────────────────────────────────────")
	fmt.Fprintln(w, "")

	// ── Table: per-record details ───────────────────────────────────────────
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Drive\tSource Path\tOriginal\tConverted\tNote/Error\tCodec\tDate\n")
	fmt.Fprintf(tw, "─────\t───────────\t────────\t─────────\t──────────\t─────\t────\n")
	for _, r := range records {
		noteOrErr := r.Note
		if r.Error != "" {
			noteOrErr = "ERR: " + r.Error
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.DriveRoot, r.SourcePath,
			formatBytes(r.OriginalSize), formatBytes(r.ConvertedSize),
			noteOrErr, r.SourceCodec, r.ConvertedAt)
	}
	return tw.Flush()
}

// RenderNotBeneficial writes conversions where the output was larger.
func RenderNotBeneficial(ctx context.Context, w io.Writer, store database.Store, drive string) error {
	records, err := store.GetNotBeneficial(ctx, drive)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(w, "No not-beneficial conversions found.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Drive\tSource Path\tOriginal\tConverted\tIncrease %%\n")
	fmt.Fprintf(tw, "─────\t───────────\t────────\t─────────\t──────────\n")
	for _, r := range records {
		increase := 0.0
		if r.OriginalSize > 0 {
			increase = float64(r.ConvertedSize-r.OriginalSize) / float64(r.OriginalSize) * 100
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%.1f%%\n",
			r.DriveRoot, r.SourcePath,
			formatBytes(r.OriginalSize), formatBytes(r.ConvertedSize),
			increase)
	}
	return tw.Flush()
}

// RenderFormats writes the source-format breakdown.
func RenderFormats(ctx context.Context, w io.Writer, store database.Store, drive string) error {
	records, err := store.GetFormatBreakdown(ctx, drive)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(w, "No data found.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Codec\tContainer\tCount\tTotal Original\tTotal Converted\n")
	fmt.Fprintf(tw, "─────\t─────────\t─────\t──────────────\t───────────────\n")
	for _, r := range records {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			r.SourceCodec, r.SourceContainer, r.Count,
			formatBytes(r.TotalOriginal), formatBytes(r.TotalConverted))
	}
	return tw.Flush()
}

// RenderSpaceSaved writes the space-saved report for the given period.
func RenderSpaceSaved(ctx context.Context, w io.Writer, store database.Store, period string) error {
	result, err := store.GetSpaceSaved(ctx, period)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Space Saved (%s)\n", result.Period)
	fmt.Fprintln(w, "─────────────────────────────────")
	fmt.Fprintf(w, "Files:      %d\n", result.FileCount)
	fmt.Fprintf(w, "Saved:      %s\n", formatBytes(result.BytesSaved))
	return nil
}

// ── CLI subcommand wrappers ─────────────────────────────────────────────────
// Each Run* parses subcommand flags, opens the store, and delegates to the
// matching Render* function.

func RunStats(args []string) {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	dbPath := fs.String("db-path", defaultDBPath(), "Path to SQLite database")
	drive := fs.String("drive", "", "Filter by drive root (e.g. D:\\)")
	fs.Parse(args)

	store, cleanup := openStore(*dbPath)
	defer cleanup()

	if err := RenderStats(context.Background(), os.Stdout, store, *drive); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func RunErrors(args []string) {
	fs := flag.NewFlagSet("errors", flag.ExitOnError)
	dbPath := fs.String("db-path", defaultDBPath(), "Path to SQLite database")
	drive := fs.String("drive", "", "Filter by drive root (e.g. D:\\)")
	pathFilter := fs.String("path", "", "Filter by source path (SQL LIKE pattern, e.g. %Movies%)")
	fs.Parse(args)

	store, cleanup := openStore(*dbPath)
	defer cleanup()

	if err := RenderErrors(context.Background(), os.Stdout, store, *drive, *pathFilter); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func RunRecent(args []string) {
	fs := flag.NewFlagSet("recent", flag.ExitOnError)
	dbPath := fs.String("db-path", defaultDBPath(), "Path to SQLite database")
	limit := fs.Int("limit", 10, "Number of recent records to show")
	fs.Parse(args)

	store, cleanup := openStore(*dbPath)
	defer cleanup()

	if err := RenderRecent(context.Background(), os.Stdout, store, *limit); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func RunNotBeneficial(args []string) {
	fs := flag.NewFlagSet("not-beneficial", flag.ExitOnError)
	dbPath := fs.String("db-path", defaultDBPath(), "Path to SQLite database")
	drive := fs.String("drive", "", "Filter by drive root (e.g. D:\\)")
	fs.Parse(args)

	store, cleanup := openStore(*dbPath)
	defer cleanup()

	if err := RenderNotBeneficial(context.Background(), os.Stdout, store, *drive); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func RunFormats(args []string) {
	fs := flag.NewFlagSet("formats", flag.ExitOnError)
	dbPath := fs.String("db-path", defaultDBPath(), "Path to SQLite database")
	drive := fs.String("drive", "", "Filter by drive root (e.g. D:\\)")
	fs.Parse(args)

	store, cleanup := openStore(*dbPath)
	defer cleanup()

	if err := RenderFormats(context.Background(), os.Stdout, store, *drive); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func RunSpaceSaved(args []string) {
	fs := flag.NewFlagSet("space-saved", flag.ExitOnError)
	dbPath := fs.String("db-path", defaultDBPath(), "Path to SQLite database")
	period := fs.String("period", "total", "Time period: week, month, total")
	fs.Parse(args)

	store, cleanup := openStore(*dbPath)
	defer cleanup()

	if err := RenderSpaceSaved(context.Background(), os.Stdout, store, *period); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func RunDashboard(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	dbPath := fs.String("db-path", defaultDBPath(), "Path to SQLite database")
	output := fs.String("output", "", "Output HTML file path (default: dashboard.html next to executable)")
	noBrowser := fs.Bool("no-browser", false, "Don't open the dashboard in a browser")
	fs.Parse(args)

	store, cleanup := openStore(*dbPath)
	defer cleanup()

	outPath, err := dashboard.Generate(context.Background(), store, *output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating dashboard: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Dashboard generated: %s\n", outPath)

	if !*noBrowser {
		if err := openURL(outPath); err != nil {
			fmt.Fprintf(os.Stderr, "Could not open browser: %v\n", err)
		}
	}
}
