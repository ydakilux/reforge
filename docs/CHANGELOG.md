# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

## [Unreleased]

### Added
- **Runs database** — every conversion invocation creates a row in a new `runs` SQL table; every `conversions` record now carries a `run_id` linking it back. Legacy databases are auto-migrated by clustering existing records on `converted_at` (>10 min gap = new run) into synthesised runs tagged `note='legacy'`.
- **Interactive launcher menu** — running `reforge.exe` with no arguments drops into a Bubble Tea menu: Start conversion, Recent runs, stats, errors, recent, not-beneficial, formats, space-saved, dashboard, quit. Stats render into a scrollable viewport; any key returns to the menu.
- **Recent runs browser** — two-pane interactive view of all runs. Left pane lists runs newest-first with size totals; right pane shows the selected run's meta (timestamps, encoder, output drive, source paths, stats) plus the full file list. Tab toggles focus, PgUp/PgDn scrolls, Esc returns.
- **Destination-drive space warning** — setup TUI raises a warning step when the chosen output drive's free space is less than the worst-case bytes that will be written there. Suppressed when no risk is detected.
- **Source/destination echo in summary** — the final TUI summary echoes resolved source paths and destination folders so users can verify them before dismissing the screen.
- Store interface: `CreateRun`, `FinalizeRun`, `GetRuns`, `GetRunFiles` methods plus `RunInfo`, `RunStats`, `RunSummary`, `RunFileRecord` types.

### Changed
- Subcommand handlers in `internal/app/subcommands.go` extracted into reusable `Render*` functions (`RenderStats`, `RenderErrors`, `RenderRecent`, `RenderNotBeneficial`, `RenderFormats`, `RenderSpaceSaved`) so the launcher can render them into a viewport instead of duplicating SQL.
- `types.Record` gains `RunID int64` (`json:"run_id,omitempty"`); records with `RunID == 0` are written verbatim (orphan rows from prescan or pre-runs era).

### Fixed
- Runs browser: View targets `m.height - 1` rows so the bottom row stays free for bubbletea's cursor / alt-screen reset. Writing the full height scrolled the viewport on the next render and left stale lines (duplicate scroll indicators visible simultaneously when crossing the meta-to-files boundary).
- Runs browser: pre-rendered ANSI markers (`runsStyleSelected.Render("❯ ")`) are no longer embedded inside another `style.Render` call. Nested ANSI made lipgloss miscount the visible line width and wrap inside the pane, overflowing the pane height.
- Runs browser: `Errors:` stat line now colours only the integer value, fixing the mis-indentation of the following `Original:` line caused by the ANSI reset interacting with subsequent plain text.
- Runs browser: `truncate` is visible-cell-width aware (uses `lipgloss.Width`) so multi-byte runes (→, …, ✓, ✗, ·) no longer push file rows past the pane's content width.
- Runs browser: Up/Down/PgUp/PgDn/Home/End move only the scroll viewport; the separate file-cursor that fought the manual scroll position (snapping the viewport back to the cursor after PgDn) is gone.

---

## [0.9.1] - 2026-04-14

### Added
- **Encoder detection cache** — auto-detected encoder is persisted in `benchmark_cache.json` so subsequent launches skip trial encodes entirely (same 30-day expiry as benchmark results, respects `--rebenchmark`)
- **Benchmark progress in TUI** — `runBenchmarks()` streams status messages ("Benchmarking GPU…", "Using cached results", "Running parallel sweep…") to the startup screen spinner

---

## [0.9.0] - 2026-04-14

### Added
- **SQLite conversion database** replacing per-drive JSON caching — centralized `conversions.db` next to executable, WAL mode, pure Go via `modernc.org/sqlite` (no CGO)
- `Store` interface with 11 methods + `SQLiteStore` implementation in `internal/database`
- Enriched `Record` metadata: source codec, container, resolution (width/height), duration, conversion timestamp, conversion duration, source file path
- Six CLI query subcommands: `stats`, `errors`, `recent`, `not-beneficial`, `formats`, `space-saved`
- `dashboard` subcommand — generates a self-contained interactive HTML dashboard (ECharts) with KPI cards, space savings chart, conversion timeline, format breakdown, sortable tables, and per-drive filtering; auto-opens in default browser
- `--db-path` flag to specify custom SQLite database location
- `cmd/download-sample` — cross-platform Big Buck Bunny sample video downloader with interactive menu (6 options: 3 x 10s clips, 3 x full movie at 360p/720p/1080p)
- `make download-sample` Makefile target
- `make test` auto-ensures sample files exist before running (via `--ensure` flag)
- 33 database tests (9 CRUD + 22 query + 2 edge cases), all passing with `-race`
- Comprehensive test suite: 13 packages, 100+ tests, `go test -race ./...` clean
- `internal/tui` package tests (Bubble Tea model, key handling, render helpers)
- `internal/pipeline/control` tests (pause/resume, stop variants, suspend-fn callbacks, concurrency)
- `internal/ffmpeg` integration tests (real ffprobe/ffmpeg via `lookupExe` helper)
- `internal/gpu/benchmark` tests (`IsCacheValid`, `SaveCache`, `LoadCache` edge cases)
- `compat_test.go` helpers: `getDriveRoot`, `sanitizeFolderName`, `formatBytes`, `fmtElapsed`, `buildConversionArgs`
- `Makefile` targets: `test-short`, `test-cover`, `cover-html`

### Changed
- Conversion cache backend: per-drive JSON files replaced by centralized SQLite database
- `Record` struct expanded from 5 fields to 13 fields (backward-compatible JSON tags)
- `main.go` dispatches subcommands via `os.Args[1]` before `flag.Parse` (no CLI framework)

### Fixed
- `internal/ffmpeg/ffmpeg_test.go`: `lookupExe` now prefers native Linux `ffmpeg` over Windows `ffmpeg.exe` on WSL (Windows binary cannot access Linux `/tmp/` paths)
- `internal/logging/logging_test.go`: Circuit breaker tests replaced `http://127.0.0.1:1` (hangs 5s per attempt on WSL) with closed `httptest.NewServer` for instant "connection refused"

### Removed
- Per-drive JSON `DatabaseManager` and `converted_files.json` caching
- `benchmark temp.bat`, `benchmark v.bat` (Windows batch scripts)
- Moved `CHANGELOG.md`, `QUICK_START.md`, `QUALITY_SETTINGS.md` from root to `docs/`

---

## [0.4.0] - 2026-02-28

### Added
- Interactive TUI powered by Bubble Tea v1 — live per-file progress bars, log panel, control bar
- `internal/tui` package with plain-mode fallback when stdout is not a TTY
- Pause / Resume / Stop-now / Stop-after-current keyboard controls (`p`, `s`, `q`) in TUI
- `internal/pipeline/control` — `Controller` with `CheckPause`, `SuspendFn`, `StopNow`, `StopAfterCurrent`
- `cmd/benchmark` sub-command for measuring optimal `--jobs` value across a sample folder

### Changed
- Producer-consumer pipeline wired through `Controller` for cooperative pause/stop
- Progress reporting moved from plain `log.Infof` to TUI `msgProgress` messages

---

## [0.3.0] - 2026-02-20

### Added
- GPU auto-detection at startup: NVIDIA NVENC, AMD AMF, Intel QSV
- Trial-encode verification — encoder is tested with a short clip before committing to it
- GPU benchmark: measures encoding speed, caches result, honours `--rebenchmark` flag
- Multi-GPU distribution for NVIDIA: work split proportional to benchmark throughput
- `internal/gpu/benchmark`, `internal/gpu/detect`, `internal/gpu/nvidia` packages
- `internal/fallback` — interactive or automatic CPU fallback when GPU encoding fails mid-run
- `internal/encoder` — per-encoder quality normalization (CRF, CQ, QP, global_quality)
- `--encoder` flag (`auto` | `hevc_nvenc` | `hevc_amf` | `hevc_qsv` | `libx265`)
- `--non-interactive` flag — skip GPU fallback prompt, auto-fall back to CPU
- `--rebenchmark` flag — force fresh GPU benchmark even if cached results exist
- `--jobs` flag — explicit parallel job count; `0` uses benchmark recommendation
- `--bypass` flag — re-convert files already recorded in the database
- `--force-hevc` flag — re-compress files already in H.265/HEVC format
- `--same-drive` flag — write output to source drive, skipping the drive prompt
- Three quality presets: `high_quality`, `balanced` (default), `space_saver`
- Per-resolution custom quality overrides in config (`custom_quality_sd` … `custom_quality_4k`)

### Changed
- Full package refactor into `internal/` sub-packages (`config`, `database`, `encoder`, `ffmpeg`, `gpu/*`, `fallback`, `logging`, `pipeline`, `tui`, `types`)
- Default encoder changed from `libx265` to `auto`
- Config field `video_encoder` replaces old single-encoder field

### Fixed
- Database lock promotion race condition (RLock → Lock upgrade)

---

## [0.2.0] - 2026-02-11

### Added
- Per-file conversion summary box (original size, new size, saved/increased, kept/discarded)
- Final statistics summary with files analyzed, converted, improved, discarded, skipped, errored
- `[N/Total] Analyzing:` progress line during file discovery phase

### Changed
- FFmpeg progress reporting interval changed from 5 % to 10 %
- File analysis log messages moved to DEBUG level to reduce console noise
- Timestamps removed from console output (still sent to Seq)

### Fixed
- Progress bar and log messages no longer overlap in console output

---

## [0.1.0] - 2026-02-01

### Added
- Batch video conversion to HEVC/H.265 via FFmpeg
- Per-drive BLAKE3 hash-based cache (`D:\converted_files.json`) to skip already-converted files
- Partial hash mode (16 MB start + middle + end + file size) for fast large-file identification
- Producer-consumer pipeline with configurable queue size (`max_queue_size`)
- Output structure: `<drive>\HSORTED\<source-folder>\<filename>.mp4`
- Atomic temp-file writes (`__tmp__<hash8>.mp4` → rename)
- MKV passthrough: preserves all audio/subtitle streams and metadata
- Optional Seq logging via custom `SeqHook` (logrus hook, HTTP POST, `X-Seq-ApiKey`)
- Auto-opens output folder in Explorer on completion (Windows)
- `configVideoConversion.json` auto-created with defaults on first run
- `--dry-run` flag — preview without writing any files
- `--config` flag — path to config file

[Unreleased]: https://github.com/ydakilux/reforge/compare/v0.9.1...HEAD
[0.9.1]: https://github.com/ydakilux/reforge/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/ydakilux/reforge/compare/v0.4.0...v0.9.0
[0.4.0]: https://github.com/ydakilux/reforge/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/ydakilux/reforge/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ydakilux/reforge/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ydakilux/reforge/releases/tag/v0.1.0
