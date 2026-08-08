// Package app wires all subsystems together and exposes a single Run entry point.
// It replaces the package-level globals that previously lived in main.go.
package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	cfgpkg "github.com/ydakilux/reforge/internal/config"
	"github.com/ydakilux/reforge/internal/converter"
	"github.com/ydakilux/reforge/internal/database"
	"github.com/ydakilux/reforge/internal/discovery"
	"github.com/ydakilux/reforge/internal/encoder"
	"github.com/ydakilux/reforge/internal/fallback"
	"github.com/ydakilux/reforge/internal/fileutil"
	"github.com/ydakilux/reforge/internal/gpu/benchmark"
	"github.com/ydakilux/reforge/internal/gpu/detect"
	"github.com/ydakilux/reforge/internal/gpu/nvidia"
	"github.com/ydakilux/reforge/internal/logging"
	"github.com/ydakilux/reforge/internal/pipeline"
	"github.com/ydakilux/reforge/internal/tui"
	"github.com/ydakilux/reforge/internal/types"
)

// MaxParallelJobsCap is the hard upper bound on concurrent conversion jobs.
const MaxParallelJobsCap = 8

// AppVersion is set by main.go at startup via the version variable injected
// by -ldflags. It is exposed here so TUI components can display it without
// requiring the version to be threaded through every function signature.
var AppVersion = "0.9.1"

// Options holds the parsed CLI flags.
type Options struct {
	ConfigFile     string
	DryRun         bool
	Bypass         bool
	ForceHevc      bool
	SameDrive      bool
	EncoderName    string
	ParallelJobs   int
	NonInteractive bool
	Rebenchmark    bool
	DBPath         string
	Paths          []string
}

// setupResult carries the values produced by runSetupPhase that the
// conversion phase needs.
type setupResult struct {
	paths     []string
	bypass    bool
	forceHevc bool
}

// App holds all runtime state, replacing the package-level globals in main.go.
type App struct {
	opts            Options
	config          types.Config
	execDir         string
	configFilePath  string
	log             *logrus.Logger
	logCleanup      func()
	logFlush        func(serverURL, apiKey, execDir string, seqEnabled bool, w io.Writer) (*logrus.Logger, func())
	store           database.Store
	stats           types.Stats
	selectedEncoder encoder.Encoder
	encoderRegistry *encoder.Registry
	fbManager       *fallback.FallbackManager
	ui              *tui.UI
	pipeline        *pipeline.Pipeline // stored so the TUI control callback can call Stop()
	pipelineCtrl    *pipeline.Controller
	outputDrive     string          // "" = use source drive
	gpuBenchmarks   map[int]float64 // gpuIndex → FPS (nil for single-GPU or CPU)
	sourcePaths     []string        // paths actually processed (echoed in the final summary)
	runID           int64           // id of the runs row created for this invocation (0 if not yet created)
	runStartedAt    time.Time       // wall-clock start of this run (set when runID is created)
}

// New creates an App from parsed options and validates basic preconditions.
func New(opts Options) (*App, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}
	return &App{
		opts:    opts,
		execDir: filepath.Dir(exePath),
	}, nil
}

// Run executes the full conversion workflow. It is the single entry point
// called by main after flag parsing.
func (a *App) Run() error {
	sr, err := a.runSetupPhase()
	if err != nil {
		return err
	}

	if err := a.runConversionPhase(sr); err != nil {
		return err
	}

	a.runSummaryPhase()
	return nil
}

func (a *App) runSetupPhase() (setupResult, error) {
	if err := a.loadConfig(); err != nil {
		return setupResult{}, err
	}

	a.log, a.logFlush = logging.SetupEarlyLogging(a.config.LogLevel, a.execDir)

	setupStart := time.Now()
	a.log.Info("Initializing setup phase...")

	resStart := time.Now()
	ffmpegExe := cfgpkg.ResolveExecutable(a.config.FFmpegPath, cfgpkg.ExeName("ffmpeg"), a.execDir)
	a.log.Infof("Resolved FFmpeg executable: %q (%v elapsed)", ffmpegExe, time.Since(resStart))

	if ffmpegExe == "" {
		if err := a.promptInstallFFmpeg(); err != nil {
			return setupResult{}, err
		}
	}

	a.fbManager = fallback.NewFallbackManager(!a.config.NonInteractive, os.Stdin, a.log)

	encStart := time.Now()
	a.log.Infof("Selecting video encoder (config setting: %s)...", a.config.VideoEncoder)
	if err := a.selectEncoder(ffmpegExe); err != nil {
		return setupResult{}, err
	}
	a.log.Infof("Encoder selection complete (%v elapsed)", time.Since(encStart))

	// Pre-load cached benchmark recommendation so the TUI displays the true recommended parallel job count.
	if cache, err := benchmark.LoadCache(a.configFilePath); err == nil && cache != nil {
		parallelKey := benchmark.ParallelCacheKey(a.config.VideoEncoder)
		if benchmark.IsParallelCacheValid(cache, parallelKey) {
			if cached, ok := cache.ParallelResults[parallelKey]; ok && cached.BestParallelism > 0 {
				if a.config.MaxParallelJobs <= 1 {
					a.config.MaxParallelJobs = cached.BestParallelism
					a.log.Infof("Loaded benchmark recommendation: %d parallel job(s) for %s", cached.BestParallelism, a.config.VideoEncoder)
				}
			}
		}
	}

	startupCh := make(chan string, 16)

	// When paths are already known (CLI args), do a fast pre-discovery (directory
	// walk only — no FFprobe) so we can show the total file size on the output-drive
	// selection screen before the user picks a drive.
	var preDiscoveredTotalBytes int64
	preBytesPerDrive := make(map[string]int64)
	if len(a.opts.Paths) > 0 {
		discStart := time.Now()
		a.log.Infof("Running pre-discovery scan on %d path(s)...", len(a.opts.Paths))
		preFiles, _ := discovery.DiscoverFiles(a.opts.Paths, a.config.FileExtensions, a.log)
		for _, f := range preFiles {
			if info, err := os.Stat(f); err == nil {
				preDiscoveredTotalBytes += info.Size()
				preBytesPerDrive[fileutil.GetDriveRoot(f)] += info.Size()
			}
		}
		a.log.Infof("Pre-discovery complete: %d file(s), %d bytes (%v elapsed)", len(preFiles), preDiscoveredTotalBytes, time.Since(discStart))
	}
	a.log.Infof("Setup phase initialized in %v", time.Since(setupStart))

	drivesCh := make(chan []tui.DriveInfo, 1)

	setupOpts := tui.SetupOptions{
		StartupCh:          startupCh,
		DrivesCh:           drivesCh,
		NeedFolder:         len(a.opts.Paths) == 0,
		VideoExtensions:    a.config.FileExtensions,
		NeedBypass:         !a.opts.Bypass,
		NeedForceHevc:      !a.opts.ForceHevc,
		NeedParallelJobs:   a.opts.ParallelJobs == 0,
		DefaultParallel:    a.config.MaxParallelJobs,
		NeedOutputDrive:    !a.opts.SameDrive,
		TotalFileSizeBytes: preDiscoveredTotalBytes,
		BytesPerDrive:      preBytesPerDrive,
	}

	var answersCh <-chan tui.SetupAnswers

	tuiInitStart := time.Now()
	a.log.Info("Initializing TUI interface...")
	a.ui, answersCh = tui.New(setupOpts, func(action tui.ControlAction) {
		if a.pipelineCtrl == nil {
			return
		}
		switch action {
		case tui.ActionPause:
			if a.pipelineCtrl.IsPaused() {
				a.pipelineCtrl.Resume()
				a.ui.SendControlState(false, tui.StopKindNone)
			} else {
				a.pipelineCtrl.Pause()
				a.ui.SendControlState(true, tui.StopKindNone)
			}
		case tui.ActionStopAfterCurrent:
			a.pipelineCtrl.StopAfterCurrent()
			a.ui.SendControlState(false, tui.StopKindAfterCurrent)
		case tui.ActionStopNow:
			a.pipelineCtrl.StopNow()
			if a.pipeline != nil {
				a.pipeline.Stop()
			}
			a.ui.SendControlState(false, tui.StopKindNow)
		}
	})
	a.log.Infof("TUI interface initialized (%v elapsed)", time.Since(tuiInitStart))

	startupErrCh := make(chan error, 1)
	go func() {
		defer close(startupCh)
		defer close(drivesCh)

		driveStart := time.Now()
		a.log.Info("Detecting available drives asynchronously...")
		drives := getAvailableDrives()
		drivesCh <- drives
		a.log.Infof("Drive detection complete: %d drive(s) found (%v elapsed)", len(drives), time.Since(driveStart))

		startupCh <- fmt.Sprintf("Encoder: %s", a.config.VideoEncoder)

		if err := a.runBenchmarks(ffmpegExe, startupCh); err != nil {
			startupErrCh <- err
			return
		}
		startupCh <- fmt.Sprintf("Ready  (parallel jobs: %d)", a.config.MaxParallelJobs)
		startupErrCh <- nil
	}()

	tuiWaitStart := time.Now()
	a.log.Info("Rendering setup UI form and awaiting user input...")

	answers := <-answersCh
	a.log.Infof("Setup UI form completed by user (%v elapsed)", time.Since(tuiWaitStart))
	if answers.Cancelled {
		a.ui.Wait()
		go func() { <-startupErrCh }()
		return setupResult{}, fmt.Errorf("setup cancelled by user")
	}

	if err := <-startupErrCh; err != nil {
		a.ui.Wait()
		return setupResult{}, err
	}

	if answers.ParallelJobs > 0 {
		a.config.MaxParallelJobs = answers.ParallelJobs
	} else if a.opts.ParallelJobs > 0 {
		if a.opts.ParallelJobs > MaxParallelJobsCap {
			a.opts.ParallelJobs = MaxParallelJobsCap
		}
		a.config.MaxParallelJobs = a.opts.ParallelJobs
	}
	if answers.OutputDrive != "" {
		a.outputDrive = answers.OutputDrive
	}
	paths := a.opts.Paths
	if len(paths) == 0 {
		paths = answers.Paths
	}
	if len(paths) == 0 {
		a.ui.Wait()
		return setupResult{}, fmt.Errorf("no input path provided")
	}

	return setupResult{
		paths:     paths,
		bypass:    a.opts.Bypass || answers.Bypass,
		forceHevc: a.opts.ForceHevc || answers.ForceHevc,
	}, nil
}

func (a *App) runConversionPhase(sr setupResult) error {
	a.sourcePaths = append(a.sourcePaths[:0], sr.paths...)

	if a.logFlush != nil {
		a.log, a.logCleanup = a.logFlush(a.config.Seq.ServerURL, a.config.Seq.APIKey, a.execDir, a.config.Seq.Enabled, a.ui.Writer())
		a.logFlush = nil
	}

	dbPath := a.opts.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(a.execDir, "conversions.db")
	}
	store, err := database.NewSQLiteStore(dbPath, a.log)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()
	a.store = store
	a.stats.TouchedDrives = make(map[string]bool)

	// Create the run row up front so its id can be attached to every
	// conversion record written by the converter. Failure to create the row
	// is non-fatal: we log and continue with runID=0 (records remain
	// orphan, just like in the pre-runs era).
	a.runStartedAt = time.Now().UTC()
	runID, runErr := a.store.CreateRun(context.Background(), database.RunInfo{
		StartedAt:     a.runStartedAt.Format(time.RFC3339),
		SourcePaths:   sr.paths,
		OutputDrive:   a.outputDrive,
		Encoder:       a.config.VideoEncoder,
		QualityPreset: a.config.QualityPreset,
		ParallelJobs:  a.config.MaxParallelJobs,
	})
	if runErr != nil {
		a.log.Warnf("Failed to create run row (records will not be grouped): %v", runErr)
	} else {
		a.runID = runID
	}

	a.log.Info("Discovering files...")
	files, fileToBaseDir := discovery.DiscoverFiles(sr.paths, a.config.FileExtensions, a.log)
	if len(files) == 0 {
		a.log.Info("No files found")
		return nil
	}
	a.log.Infof("Found %d files to process", len(files))

	// Sum file sizes for the global ETA.
	var totalDiscoveredBytes int64
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			totalDiscoveredBytes += info.Size()
		}
	}
	a.ui.SetConversionStats(len(files), totalDiscoveredBytes)

	numWorkers := a.config.MaxParallelJobs
	if numWorkers < 1 {
		numWorkers = 1
	}

	gpuAssigner := pipeline.NewGPUAssigner(a.gpuBenchmarks)
	if gpuAssigner != nil && gpuAssigner.NumGPUs() > 1 {
		numWorkers = gpuAssigner.NumGPUs() * a.config.MaxEncodesPerGPU
		if numWorkers > MaxParallelJobsCap {
			numWorkers = MaxParallelJobsCap
		}
		if numWorkers < 1 {
			numWorkers = 1
		}
		a.log.Infof("Multi-GPU: %d GPUs × %d encodes/GPU = %d workers", gpuAssigner.NumGPUs(), a.config.MaxEncodesPerGPU, numWorkers)
	}
	bufSize := numWorkers * 2
	if a.config.MaxQueueSize > bufSize {
		bufSize = a.config.MaxQueueSize
	}

	ctx := context.Background()
	a.pipeline = pipeline.NewPipeline(ctx, numWorkers, bufSize)
	a.pipelineCtrl = a.pipeline.Controller()

	if numWorkers > 1 {
		a.log.Infof("Parallel mode: %d concurrent jobs", numWorkers)
	}

	conv := &converter.Converter{
		Config:              &a.config,
		ExecDir:             a.execDir,
		SelectedEncoder:     a.selectedEncoder,
		EncoderRegistry:     a.encoderRegistry,
		FallbackManager:     a.fbManager,
		DB:                  a.store,
		UI:                  a.ui,
		Stats:               &a.stats,
		Ctrl:                a.pipelineCtrl,
		OutputDriveOverride: a.outputDrive,
		Log:                 a.log,
		RunID:               a.runID,
	}

	a.pipeline.Start(func(ctx context.Context, job types.Job) pipeline.ConversionResult {
		conv.Process(ctx, job, a.opts.DryRun)
		return pipeline.ConversionResult{Job: job}
	})

	a.ui.StartTimer()

	go func() {
		discovery.Produce(files, fileToBaseDir, a.pipeline, sr.bypass, sr.forceHevc, discovery.ProducerConfig{
			Config:      &a.config,
			ExecDir:     a.execDir,
			DB:          a.store,
			Stats:       &a.stats,
			Log:         a.log,
			GPUAssigner: gpuAssigner,
			RunID:       a.runID,
			OnFileFinished: func(sizeBytes int64) {
				a.ui.FileFinished(sizeBytes)
			},
		})
	}()

	for result := range a.pipeline.Results() {
		a.ui.FileFinished(result.Job.OriginalSize)
	}

	a.finalizeRun()

	return nil
}

// finalizeRun writes the end timestamp and aggregate counters onto the run row
// created at the start of runConversionPhase. Safe to call when runID == 0
// (no-op) or when the store is unavailable (logs and returns).
func (a *App) finalizeRun() {
	if a.runID == 0 || a.store == nil {
		return
	}
	a.stats.Mu.Lock()
	stats := database.RunStats{
		EndedAt:        time.Now().UTC().Format(time.RFC3339),
		FileCount:      a.stats.FilesProcessed,
		OriginalBytes:  a.stats.OriginalBytes,
		ConvertedBytes: a.stats.FinalBytes,
		ErrorCount:     a.stats.FilesErrored,
		NotBeneficial:  a.stats.FilesDiscarded,
	}
	a.stats.Mu.Unlock()
	if err := a.store.FinalizeRun(context.Background(), a.runID, stats); err != nil {
		a.log.Warnf("Failed to finalize run %d: %v", a.runID, err)
	}
}

func (a *App) runSummaryPhase() {
	elapsed := a.ui.Elapsed()
	tuiSummary := a.buildStatsSummary(elapsed)

	for _, line := range tuiSummary {
		a.log.Info(line)
	}

	a.ui.ShowSummary(tuiSummary)

	boxSummary := a.buildStatsSummaryBox(elapsed)
	a.ui.PrintSummary(boxSummary)

	openREFORGEDFolders(a.stats.TouchedDrives, a.outputDrive)

	fmt.Println("All tasks completed")
	fmt.Println()
	if a.config.NonInteractive {
		fmt.Printf("ELAPSED=%s\n", fileutil.FmtElapsed(elapsed))
	}
}

// ── private helpers ────────────────────────────────────────────────────────────

func (a *App) loadConfig() error {
	configPath := a.opts.ConfigFile
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(a.execDir, configPath)
	}
	cfg, err := cfgpkg.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	a.config = cfg
	a.configFilePath = configPath

	if a.opts.NonInteractive {
		a.config.NonInteractive = true
	}
	if a.opts.Rebenchmark {
		a.config.Rebenchmark = true
	}
	if a.opts.EncoderName != "auto" {
		a.config.VideoEncoder = a.opts.EncoderName
	} else if a.config.VideoEncoder == "" {
		a.config.VideoEncoder = "auto"
	}
	cfgpkg.ApplyGPUDefaults(&a.config)
	return nil
}

func (a *App) selectEncoder(ffmpegExe string) error {
	a.encoderRegistry = encoder.NewRegistry()
	a.encoderRegistry.Register(encoder.NewLibx265Encoder())
	a.encoderRegistry.Register(encoder.NewNvencEncoder())
	a.encoderRegistry.Register(encoder.NewAmfEncoder())
	a.encoderRegistry.Register(encoder.NewQsvEncoder())

	requestedEncoder := a.config.VideoEncoder
	fellBackToCPU := false
	usedDetectionCache := false

	if a.config.VideoEncoder == "auto" {
		// Check detection cache before running expensive trial encodes.
		cache, _ := benchmark.LoadCache(a.configFilePath)
		if !a.config.Rebenchmark && benchmark.IsDetectionCacheValid(cache) {
			a.config.VideoEncoder = cache.Detection.BestEncoder
			a.log.Infof("Using cached encoder detection: %s", a.config.VideoEncoder)
			usedDetectionCache = true
		} else {
			a.log.Info("Auto-detecting GPU encoders...")
			detectionResult, err := detect.DetectEncoders(ffmpegExe, a.log)
			if err != nil {
				a.log.Warnf("GPU detection failed: %v", err)
				a.config.VideoEncoder = "libx265"
				fellBackToCPU = true
			} else {
				for _, gpu := range detectionResult.Available {
					if gpu.Available {
						a.log.Infof("  %-14s  %-20s  available (%dms)", gpu.Encoder, gpu.Name, gpu.TrialEncodeMs)
					} else {
						a.log.Infof("  %-14s  %-20s  unavailable", gpu.Encoder, gpu.Name)
					}
				}
				best := detect.SelectBest(detectionResult)
				a.config.VideoEncoder = best.Encoder
				if best.Encoder == "libx265" {
					fellBackToCPU = true
				}
				a.log.Infof("Best encoder: %s (%s)", best.Encoder, best.Name)
			}

			// Persist detection result so future launches skip trial encodes.
			if cache == nil {
				cache = &benchmark.BenchmarkCache{
					Results:         make(map[string]benchmark.BenchmarkResult),
					ParallelResults: make(map[string]benchmark.ParallelBenchmarkResult),
					Version:         "1",
				}
			}
			cache.Detection = &benchmark.DetectionCache{
				BestEncoder: a.config.VideoEncoder,
				Timestamp:   time.Now(),
			}
			if err := benchmark.SaveCache(a.configFilePath, cache); err != nil {
				a.log.Warnf("Failed to save detection cache: %v", err)
			}
		}
	}

	// Verify explicitly configured GPU encoders actually work on this system.
	// The config may have been written on a different OS (e.g. Windows) where
	// the encoder was available, but the current host (e.g. WSL/Linux) may
	// lack the required hardware or FFmpeg codec support.
	// Skip when the encoder came from the detection cache (already verified).
	if !usedDetectionCache && a.config.VideoEncoder != "auto" && a.config.VideoEncoder != "libx265" {
		if candidate, found := a.encoderRegistry.Get(a.config.VideoEncoder); found {
			if !candidate.IsAvailable(ffmpegExe) {
				a.log.Warnf("Configured encoder %q is not available on this system", a.config.VideoEncoder)
				// Probe the actual reason so we can surface a helpful hint.
				// Run the trial encode stderr through ParseError to get a specific message.
				a.log.Warnf("Hint: if you are using FFmpeg 9, NVIDIA driver ≥ 610 is required for NVENC (API 13.1)")
				a.config.VideoEncoder = "libx265"
				fellBackToCPU = true
			}
		}
	}

	enc, ok := a.encoderRegistry.Get(a.config.VideoEncoder)
	if !ok {
		a.log.Warnf("Unknown encoder %q, falling back to libx265", a.config.VideoEncoder)
		enc, _ = a.encoderRegistry.Get("libx265")
		a.config.VideoEncoder = "libx265"
		fellBackToCPU = true
	}
	a.selectedEncoder = enc

	if fellBackToCPU && requestedEncoder != "libx265" {
		if err := a.confirmCPUFallback(); err != nil {
			return err
		}
	}

	if requestedEncoder == "auto" {
		a.proposeFixEncoder(a.config.VideoEncoder)
	}

	a.log.Infof("Selected encoder: %s", a.config.VideoEncoder)
	return nil
}

func (a *App) proposeFixEncoder(bestEncoder string) {
	if a.config.NonInteractive {
		return
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "VideoEncoder is currently set to \"auto\". Auto-detected best encoder: %s\n", bestEncoder)
	fmt.Fprintf(os.Stderr, "Fixing \"video_encoder\": %q in your config file will speed up application startup on future runs.\n", bestEncoder)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "Save %q as your default video_encoder in %s? [Y/n] ", bestEncoder, filepath.Base(a.configFilePath))

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer == "" || answer == "y" || answer == "yes" {
		a.config.VideoEncoder = bestEncoder
		if err := cfgpkg.SaveConfig(a.configFilePath, a.config); err != nil {
			a.log.Warnf("Failed to update config file: %v", err)
		} else {
			a.log.Infof("Saved video_encoder: %s to %s", bestEncoder, a.configFilePath)
		}
	}
}

func (a *App) confirmCPUFallback() error {
	if a.config.NonInteractive {
		a.log.Warn("No GPU encoder available — continuing with CPU (libx265) in non-interactive mode")
		return nil
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "No GPU encoder is available on this system.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Possible causes:")
	fmt.Fprintln(os.Stderr, "    • FFmpeg 9 requires NVIDIA driver ≥ 610 (NVENC API 13.1)")
	fmt.Fprintln(os.Stderr, "      Check your driver version: nvidia-smi")
	fmt.Fprintln(os.Stderr, "      Download: https://www.nvidia.com/Download/index.aspx")
	fmt.Fprintln(os.Stderr, "    • No compatible GPU is present or the driver is not installed")
	fmt.Fprintln(os.Stderr, "    • Running inside WSL without CUDA-on-WSL driver support")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "CPU encoding (libx265) will be used instead. This is significantly slower.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprint(os.Stderr, "Continue with CPU encoding? [Y/n] ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer == "" || answer == "y" || answer == "yes" {
		return nil
	}
	return fmt.Errorf("aborted — no GPU encoder available")
}

func (a *App) runBenchmarks(ffmpegExe string, startupCh chan<- string) error {
	if a.config.VideoEncoder == "libx265" {
		return nil
	}

	progress := func(msg string) { startupCh <- msg }

	qualityArgs := a.selectedEncoder.QualityArgs(a.config.QualityPreset, 1920)
	cache, _ := benchmark.LoadCache(a.configFilePath)
	if cache == nil {
		cache = &benchmark.BenchmarkCache{
			Results:         make(map[string]benchmark.BenchmarkResult),
			ParallelResults: make(map[string]benchmark.ParallelBenchmarkResult),
			Version:         "1",
		}
	}

	// Multi-GPU: enumerate NVIDIA GPUs and benchmark each one individually.
	if a.config.VideoEncoder == "hevc_nvenc" {
		gpus, err := nvidia.QueryGPUs(a.log)
		if err == nil && len(gpus) > 1 {
			a.log.Infof("Detected %d NVIDIA GPUs — benchmarking each", len(gpus))
			progress(fmt.Sprintf("Detected %d NVIDIA GPUs", len(gpus)))
			a.gpuBenchmarks = make(map[int]float64, len(gpus))

			for i, gpu := range gpus {
				deviceArgs := a.selectedEncoder.DeviceArgs(gpu.Index)
				key := benchmark.CacheKey(a.config.VideoEncoder, gpu.Name, "")

				if !a.config.Rebenchmark && benchmark.IsCacheValid(cache, key) {
					cached := cache.Results[key]
					a.log.Infof("Using cached benchmark for GPU %d (%s): %.1f FPS", gpu.Index, gpu.Name, cached.FPS)
					progress(fmt.Sprintf("GPU %d: %.0f FPS (cached)", gpu.Index, cached.FPS))
					a.gpuBenchmarks[gpu.Index] = cached.FPS
					continue
				}

				a.log.Infof("Benchmarking GPU %d (%s, %d MB VRAM)...", gpu.Index, gpu.Name, gpu.VRAMTotalMB)
				progress(fmt.Sprintf("Benchmarking GPU %d/%d (%s)…", i+1, len(gpus), gpu.Name))
				result, err := benchmark.RunBenchmark(ffmpegExe, a.config.VideoEncoder, gpu.Index, qualityArgs, a.log, deviceArgs)
				if err != nil {
					a.log.Warnf("Benchmark failed for GPU %d (%s): %v — skipping this GPU", gpu.Index, gpu.Name, err)
					continue
				}
				a.log.Infof("GPU %d (%s): %.1f FPS (%.2fx realtime)", gpu.Index, gpu.Name, result.FPS, result.SpeedX)
				progress(fmt.Sprintf("GPU %d: %.0f FPS", gpu.Index, result.FPS))
				result.CacheKey = key
				cache.Results[key] = *result
				a.gpuBenchmarks[gpu.Index] = result.FPS
			}

			if len(a.gpuBenchmarks) == 0 {
				a.log.Warn("All GPU benchmarks failed — falling back to single-GPU mode")
				a.gpuBenchmarks = nil
			}
		}
	}

	// Single-GPU / non-NVENC benchmark (also runs when multi-GPU detection found only 1 GPU).
	singleKey := benchmark.CacheKey(a.config.VideoEncoder, "", "")
	if a.gpuBenchmarks == nil {
		if a.config.Rebenchmark || !benchmark.IsCacheValid(cache, singleKey) {
			a.log.Info("Running GPU benchmark (single stream)...")
			progress("Benchmarking GPU speed…")
			result, err := benchmark.RunBenchmark(ffmpegExe, a.config.VideoEncoder, 0, qualityArgs, a.log)
			if err != nil {
				a.log.Warnf("Benchmark failed: %v — proceeding without speed data", err)
			} else {
				a.log.Infof("Benchmark: %s @ %.1f FPS (%.2fx realtime)", a.config.VideoEncoder, result.FPS, result.SpeedX)
				progress(fmt.Sprintf("Benchmark: %.0f FPS (%.1fx realtime)", result.FPS, result.SpeedX))
				result.CacheKey = singleKey
				cache.Results[singleKey] = *result
			}
		} else {
			cached := cache.Results[singleKey]
			a.log.Infof("Using cached benchmark: %.1f FPS for %s", cached.FPS, a.config.VideoEncoder)
			progress(fmt.Sprintf("Benchmark: %.0f FPS (cached)", cached.FPS))
		}
	}

	// Parallel sweep (single-GPU only — multi-GPU uses per-GPU benchmarks for worker count).
	if a.gpuBenchmarks == nil {
		parallelKey := benchmark.ParallelCacheKey(a.config.VideoEncoder)
		if a.config.Rebenchmark || !benchmark.IsParallelCacheValid(cache, parallelKey) {
			a.log.Info("Running parallel performance sweep (this takes ~2 minutes)...")
			progress("Parallel sweep (1–4 streams)…")
			sweepResult, err := benchmark.RunParallelSweep(ffmpegExe, a.config.VideoEncoder, 4, qualityArgs, a.log)
			if err != nil {
				a.log.Warnf("Parallel sweep failed: %v — keeping current parallel setting", err)
			} else {
				cache.ParallelResults[parallelKey] = *sweepResult
				a.config.MaxParallelJobs = sweepResult.BestParallelism
				a.log.Infof("Auto-configured: %d parallel job(s) gives best throughput (%.1f FPS)", sweepResult.BestParallelism, sweepResult.BestFPS)
			}
		} else {
			cached := cache.ParallelResults[parallelKey]
			a.log.Infof("Using cached parallel sweep: best = %d job(s) @ %.1f FPS", cached.BestParallelism, cached.BestFPS)
			if a.config.MaxParallelJobs <= 1 {
				a.config.MaxParallelJobs = cached.BestParallelism
			}
		}
	}

	if err := benchmark.SaveCache(a.configFilePath, cache); err != nil {
		a.log.Warnf("Failed to save benchmark cache: %v", err)
	}
	return nil
}

type summaryData struct {
	filesAnalyzed  int
	filesProcessed int
	filesImproved  int
	filesDiscarded int
	filesSkipped   int
	filesErrored   int
	originalBytes  int64
	finalBytes     int64
	savedBytes     int64
	savedPct       float64
	elapsed        time.Duration
}

func (a *App) collectSummaryData(elapsed time.Duration) summaryData {
	a.stats.Mu.Lock()
	defer a.stats.Mu.Unlock()

	sd := summaryData{
		filesAnalyzed:  a.stats.FilesAnalyzed,
		filesProcessed: a.stats.FilesProcessed,
		filesImproved:  a.stats.FilesImproved,
		filesDiscarded: a.stats.FilesDiscarded,
		filesSkipped:   a.stats.FilesSkipped,
		filesErrored:   a.stats.FilesErrored,
		originalBytes:  a.stats.OriginalBytes,
		finalBytes:     a.stats.FinalBytes,
		elapsed:        elapsed,
	}
	if sd.originalBytes > 0 {
		sd.savedBytes = sd.originalBytes - sd.finalBytes
		sd.savedPct = float64(sd.savedBytes) / float64(sd.originalBytes) * 100
	}
	return sd
}

func (a *App) buildStatsSummary(elapsed time.Duration) []string {
	sd := a.collectSummaryData(elapsed)

	var lines []string
	lines = append(lines,
		"",
		"  VIDEO CONVERSION SUMMARY",
		"  ─────────────────────────────────────",
		fmt.Sprintf("  Files Analyzed:     %d", sd.filesAnalyzed),
		fmt.Sprintf("  Files Converted:    %d", sd.filesProcessed),
		fmt.Sprintf("    → Improved:       %d", sd.filesImproved),
		fmt.Sprintf("    → Discarded:      %d", sd.filesDiscarded),
		fmt.Sprintf("  Files Skipped:      %d", sd.filesSkipped),
		fmt.Sprintf("  Files Errored:      %d", sd.filesErrored),
		"  ─────────────────────────────────────",
	)

	if sd.originalBytes > 0 {
		lines = append(lines,
			fmt.Sprintf("  Original Size:      %s", fileutil.FormatBytes(sd.originalBytes)),
			fmt.Sprintf("  Final Size:         %s", fileutil.FormatBytes(sd.finalBytes)),
			fmt.Sprintf("  Space Saved:        %s  (%.1f%%)", fileutil.FormatBytes(sd.savedBytes), sd.savedPct),
		)
	}

	if sd.elapsed > 0 {
		lines = append(lines,
			"  ─────────────────────────────────────",
			fmt.Sprintf("  Total Time:         %s", fileutil.FmtElapsed(sd.elapsed)),
		)
	}

	// Echo source and destination paths so the user can verify them at a
	// glance before pressing Enter to dismiss the TUI.
	if pathLines := a.buildPathsSummary("  "); len(pathLines) > 0 {
		lines = append(lines, "  ─────────────────────────────────────")
		lines = append(lines, pathLines...)
	}

	lines = append(lines, "")
	return lines
}

// buildStatsSummaryBox returns the same data formatted as a fixed-width box for
// the post-TUI plain-text stdout print.
func (a *App) buildStatsSummaryBox(elapsed time.Duration) []string {
	sd := a.collectSummaryData(elapsed)

	var lines []string
	lines = append(lines,
		"",
		"╔════════════════════════════════════════════════════════════════╗",
		"║           VIDEO CONVERSION SUMMARY                             ║",
		"╠════════════════════════════════════════════════════════════════╣",
		fmt.Sprintf("║ Files Analyzed:     %-43d║", sd.filesAnalyzed),
		fmt.Sprintf("║ Files Converted:    %-43d║", sd.filesProcessed),
		fmt.Sprintf("║   → Improved:       %-43d║", sd.filesImproved),
		fmt.Sprintf("║   → Discarded:      %-43d║", sd.filesDiscarded),
		fmt.Sprintf("║ Files Skipped:      %-43d║", sd.filesSkipped),
		fmt.Sprintf("║ Files Errored:      %-43d║", sd.filesErrored),
		"╠════════════════════════════════════════════════════════════════╣",
	)

	if sd.originalBytes > 0 {
		lines = append(lines,
			fmt.Sprintf("║ Original Size:      %-43s║", fileutil.FormatBytes(sd.originalBytes)),
			fmt.Sprintf("║ Final Size:         %-43s║", fileutil.FormatBytes(sd.finalBytes)),
			fmt.Sprintf("║ Space Saved:        %-33s (%.1f%%)║", fileutil.FormatBytes(sd.savedBytes), sd.savedPct),
		)
	}

	if sd.elapsed > 0 {
		lines = append(lines,
			"╠════════════════════════════════════════════════════════════════╣",
			fmt.Sprintf("║ Total Time:         %-43s║", fileutil.FmtElapsed(sd.elapsed)),
		)
	}

	lines = append(lines,
		"╚════════════════════════════════════════════════════════════════╝",
		"",
	)

	// Echo source and destination paths after the box so users can verify
	// where files came from and went to. Kept outside the fixed-width box
	// because real-world paths can easily exceed the box's inner width.
	if pathLines := a.buildPathsSummary(""); len(pathLines) > 0 {
		lines = append(lines, pathLines...)
		lines = append(lines, "")
	}
	return lines
}

// buildPathsSummary returns human-readable "Source" and "Destination" sections
// for the final summary. Each output line is prefixed with indent. Returns nil
// when there is nothing meaningful to show (e.g. no source paths recorded).
func (a *App) buildPathsSummary(indent string) []string {
	if len(a.sourcePaths) == 0 {
		return nil
	}

	var lines []string
	if len(a.sourcePaths) == 1 {
		lines = append(lines, indent+"Source path:")
	} else {
		lines = append(lines, fmt.Sprintf("%sSource paths (%d):", indent, len(a.sourcePaths)))
	}
	for _, p := range a.sourcePaths {
		lines = append(lines, indent+"  • "+p)
	}

	dests := a.destinationPaths()
	if len(dests) > 0 {
		if len(dests) == 1 {
			lines = append(lines, indent+"Destination:")
		} else {
			lines = append(lines, fmt.Sprintf("%sDestinations (%d):", indent, len(dests)))
		}
		for _, d := range dests {
			lines = append(lines, indent+"  • "+d)
		}
	}
	return lines
}

// destinationPaths returns the resolved REFORGED output folders for this run.
// When the user picked an explicit output drive, that single drive's REFORGED
// folder is returned. Otherwise the REFORGED folder on each touched source
// drive is returned, sorted for stable display.
func (a *App) destinationPaths() []string {
	if a.outputDrive != "" {
		return []string{filepath.Join(a.outputDrive, converter.OutputDirName)}
	}
	a.stats.Mu.Lock()
	defer a.stats.Mu.Unlock()
	if len(a.stats.TouchedDrives) == 0 {
		return nil
	}
	dests := make([]string, 0, len(a.stats.TouchedDrives))
	for drive := range a.stats.TouchedDrives {
		dests = append(dests, filepath.Join(drive, converter.OutputDirName))
	}
	sort.Strings(dests)
	return dests
}

// ── interactive prompt helpers ─────────────────────────────────────────────────

// promptInstallFFmpeg informs the user that ffmpeg was not found and offers to
// open the download page in their browser. It always returns a non-nil error so
// that Run() exits cleanly after the prompt.
func (a *App) promptInstallFFmpeg() error {
	const downloadURL = "https://www.ffmpeg.org/download.html"

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "ERROR: ffmpeg was not found on this system.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "ffmpeg is required to convert videos. You can download it from:")
	fmt.Fprintf(os.Stderr, "  %s\n", downloadURL)
	fmt.Fprintln(os.Stderr, "")

	fmt.Fprintln(os.Stderr, "Please install ffmpeg and add it to PATH, or set ffmpeg_path in the config file.")
	if !a.config.NonInteractive {
		if err := openURL(downloadURL); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open browser: %v\n", err)
		}
	}

	return fmt.Errorf("ffmpeg not found — please install ffmpeg and add it to PATH, or set ffmpeg_path in the config file")
}

func openREFORGEDFolders(touchedDrives map[string]bool, outputDriveOverride string) {
	// When an output drive was chosen, all files land on that single drive.
	// Open only that drive's REFORGED folder; ignore the source drives.
	if outputDriveOverride != "" {
		REFORGEDPath := filepath.Join(outputDriveOverride, converter.OutputDirName)
		if _, err := os.Stat(REFORGEDPath); err == nil {
			openFolder(REFORGEDPath) //nolint:errcheck
		}
		return
	}
	for drive := range touchedDrives {
		REFORGEDPath := filepath.Join(drive, converter.OutputDirName)
		if _, err := os.Stat(REFORGEDPath); err == nil {
			openFolder(REFORGEDPath) //nolint:errcheck
		}
	}
}

// ExitWithPause pauses before exit so users can read the last output.
func ExitWithPause(code int) {
	fmt.Fprintln(os.Stderr, "\nPress Enter to exit...")
	bufio.NewReader(os.Stdin).ReadBytes('\n') //nolint:errcheck
	os.Exit(code)
}
