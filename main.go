package main

//go:generate go-winres make --product-version=git-tag --file-version=git-tag

import (
	"flag"
	"fmt"
	"os"

	"github.com/ydakilux/reforge/internal/app"
)

var (
	version = "0.9.1" // overridden at release time via -ldflags "-X main.version=x.y.z"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Propagate the build-time version to the app package so TUI components
	// can render it without needing it threaded through every call site.
	app.AppVersion = version

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Printf("reforge v%s (commit: %s, built: %s)\n", version, commit, date)
			return
		case "stats":
			app.RunStats(os.Args[2:])
			return
		case "errors":
			app.RunErrors(os.Args[2:])
			return
		case "recent":
			app.RunRecent(os.Args[2:])
			return
		case "not-beneficial":
			app.RunNotBeneficial(os.Args[2:])
			return
		case "formats":
			app.RunFormats(os.Args[2:])
			return
		case "space-saved":
			app.RunSpaceSaved(os.Args[2:])
			return
		case "dashboard":
			app.RunDashboard(os.Args[2:])
			return
		}
	} else {
		// No arguments at all: show the interactive launcher menu. The user
		// can either pick a stats view (loops until they quit) or "Start
		// conversion", which falls through to the regular conversion flow.
		if app.RunLauncher() == app.LauncherQuit {
			return
		}
	}

	// Default: existing conversion flow
	var (
		configFile     = flag.String("config", "reforge.json", "Path to config file")
		dryRun         = flag.Bool("dry-run", false, "Dry run mode")
		bypassFlag     = flag.Bool("bypass", false, "Re-convert files already in the database (bypass DB check)")
		forceHevcFlag  = flag.Bool("force-hevc", false, "Re-compress files that are already H.265/HEVC")
		sameDrive      = flag.Bool("same-drive", false, "Write output to the same drive as the source (skip drive prompt)")
		encoderFlag    = flag.String("encoder", "auto", "Video encoder (auto, hevc_nvenc, hevc_amf, hevc_qsv, libx265)")
		parallelJobs   = flag.Int("jobs", 0, "Number of parallel conversion jobs (0 = use benchmark recommendation)")
		nonInteractive = flag.Bool("non-interactive", false, "Disable interactive prompts for GPU fallback")
		rebenchmark    = flag.Bool("rebenchmark", false, "Force GPU benchmark even if cache exists")
		dbPath         = flag.String("db-path", "", "Path to SQLite database (default: conversions.db next to executable)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Reforge v%s — batch HEVC video converter\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage: %s [subcommand] [flags] [directory]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Subcommands:\n")
		fmt.Fprintf(os.Stderr, "  version         Print version information\n")
		fmt.Fprintf(os.Stderr, "  stats           Show conversion statistics\n")
		fmt.Fprintf(os.Stderr, "  errors          List failed conversions\n")
		fmt.Fprintf(os.Stderr, "  recent          Show recent conversions\n")
		fmt.Fprintf(os.Stderr, "  not-beneficial  List conversions where output was larger\n")
		fmt.Fprintf(os.Stderr, "  formats         Show source format breakdown\n")
		fmt.Fprintf(os.Stderr, "  space-saved     Show total space saved\n")
		fmt.Fprintf(os.Stderr, "\nConversion flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	a, err := app.New(app.Options{
		ConfigFile:     *configFile,
		DryRun:         *dryRun,
		Bypass:         *bypassFlag,
		ForceHevc:      *forceHevcFlag,
		SameDrive:      *sameDrive,
		EncoderName:    *encoderFlag,
		ParallelJobs:   *parallelJobs,
		NonInteractive: *nonInteractive,
		Rebenchmark:    *rebenchmark,
		DBPath:         *dbPath,
		Paths:          flag.Args(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		app.ExitWithPause(1)
	}

	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		app.ExitWithPause(1)
	}
}
