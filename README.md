# Reforge

A high-performance CLI batch video conversion tool for Windows that converts videos to HEVC/H.265 format with GPU auto-detection, intelligent caching, concurrent processing, and detailed conversion reporting.

## ✨ Key Features

- **🎬 Batch Conversion** - Process entire directories of videos automatically
- **⚡ HEVC/H.265 Encoding** - Significant file size reduction with maintained quality
- **🖥️ GPU Auto-Detection** - Automatically finds and uses the best available hardware encoder
- **🔄 Smart Caching** - SQLite database with BLAKE3 hashing prevents re-processing
- **🚀 Concurrent Processing** - Producer-consumer pipeline for efficient batch operations
- **📊 Detailed Reporting** - Per-file conversion statistics with size comparisons
- **🔍 Query Subcommands** - Built-in `stats`, `errors`, `recent`, `not-beneficial`, `formats`, `space-saved`, `dashboard`
- **📜 Runs Browser** - Run with no arguments to open an interactive menu; the **Recent runs** entry opens a two-pane browser grouping every conversion into the run that produced it
- **🎯 Quality Presets** - Configurable quality settings (high_quality, balanced, space_saver)
- **📁 Directory Preservation** - Maintains folder structure from dropped directory onwards
- **🧹 Smart Output** - Only creates directories when conversions succeed
- **🎮 Multiple Encoders** - NVIDIA NVENC, AMD AMF, Intel QSV, and CPU libx265
- **📝 Optional Seq Logging** - Integration with Seq for centralized logging
- **🖥️ Windows Optimized** - Auto-opens output folders after completion

## 🖥️ GPU Auto-Detection

The tool automatically detects your GPU and picks the fastest available encoder:

1. **Probes hardware** at startup for NVIDIA NVENC, AMD AMF, and Intel QSV support
2. **Trial-encodes a short clip** to verify the encoder actually works (not just reported as available)
3. **Benchmarks GPU speed** and caches results so future runs start instantly
4. **Falls back to CPU** (`libx265`) if no working GPU encoder is found

You don't need to configure anything. Just run the tool and it picks the best option. If you want a specific encoder, use `--encoder` to override.

### Supported Encoders

| Encoder | Hardware | Quality Flag | Speed | Notes |
|---------|----------|-------------|-------|-------|
| `auto` | (detect) | (varies) | Best available | **Default.** Picks the fastest working encoder |
| `hevc_nvenc` | NVIDIA GPU | `-cq` | Fastest | Requires NVIDIA drivers with NVENC support |
| `hevc_amf` | AMD GPU | `-qp_i`/`-qp_p` | Fast | Requires AMD drivers with AMF support |
| `hevc_qsv` | Intel GPU | `-global_quality` | Fast | Requires Intel GPU with Quick Sync |
| `libx265` | CPU | `-crf` | Slowest | Always available, most compatible |

### Multi-GPU (NVIDIA)

When multiple NVIDIA GPUs are detected, the tool distributes encoding across them based on benchmark speed. Each GPU gets work proportional to its throughput. AMD and Intel don't support device selection through FFmpeg, so multi-GPU only applies to NVIDIA setups.

## 🚀 Quick Start

1. **Drop a folder** on `reforge.exe` or run from command line:
   ```bash
   reforge.exe D:\Videos\
   ```

2. **Answer the prompts**:
   - Force re-conversion? [y/N]
   - Test HEVC files too? [y/N]
   - Output drive (optional)

3. **Watch it work**:
   ```
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   Converting [3/25]: video.mp4 (1080p)
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   Progress: 10%
   Progress: 20%
   ...
   ┌────────────────────────────────────────────────────────────────┐
   │ File: video.mp4                                                │
   ├────────────────────────────────────────────────────────────────┤
   │ Original Size:    284.66 MB                                    │
   │ New Size:         198.45 MB                                    │
   │ Saved:             86.21 MB (30.3% reduction)                  │
   ├────────────────────────────────────────────────────────────────┤
   │ Result: ✓ KEPT - File will be saved                           │
   └────────────────────────────────────────────────────────────────┘
   ```

4. **Find converted videos** at `D:\REFORGED\<your-folder>\`

## 📋 Requirements

- **Windows 10/11** or **Linux / WSL2**
- **FFmpeg** (≥ 6.0, **FFmpeg 9 recommended**) — Place in `ffmpeg\bin\` relative to exe, configure path in config, or install system-wide (`apt install ffmpeg`). Note: FFmpeg 9 requires **NVENC SDK ≥ 11.1** (NVIDIA driver ≥ 570) for GPU encoding; older drivers will fall back to CPU automatically.
- **MediaInfo CLI** - Place in `MediaInfo_CLI_24.04_Windows_x64\` or configure path (`apt install mediainfo`)
- **GPU** (optional) - NVIDIA, AMD, or Intel GPU for hardware-accelerated encoding. Falls back to CPU automatically if no GPU is available.

### WSL / Linux Notes

The tool runs natively on Linux and WSL2. A few things to be aware of:

- **GPU encoding on WSL2** requires NVIDIA GPU + the [CUDA on WSL](https://docs.nvidia.com/cuda/wsl-user-guide/) driver installed on the Windows host. AMD and Intel GPU encoding are not supported on WSL. When GPU encoding is unavailable, the tool automatically falls back to CPU (`libx265`).
- **Config portability**: if your `reforge.json` was created on Windows with a GPU encoder (e.g. `hevc_nvenc`), the tool will detect it is unavailable on WSL/Linux and fall back to `libx265` automatically.
- **Output paths**: on WSL, output is written relative to the detected mount point (e.g. `/mnt/d/REFORGED/...`). On native Linux, it uses the first meaningful path prefix (e.g. `/home/user/REFORGED/...`).

## 📦 Installation

### Option 1: Download Binary (Recommended)
1. Download `reforge.exe` from [Releases](https://github.com/ydakilux/reforge/releases)
2. **Windows security:** Right-click the downloaded `.exe` → **Properties** → check **Unblock** → OK. This is required because the file was downloaded from the internet and Windows blocks it by default.
3. Download [FFmpeg](https://ffmpeg.org/download.html) and extract to `ffmpeg\bin\` folder
4. Download [MediaInfo CLI](https://mediaarea.net/en/MediaInfo/Download/Windows) and extract
5. Run `reforge.exe` - config will be auto-created on first run

### Option 2: Build from Source
```bash
# Clone the repository
git clone <your-repo-url>
cd reforge

# Download dependencies
go mod download

# Build
go build -o reforge.exe

# Or optimized build
go build -ldflags="-s -w" -o reforge.exe
```

## 🎯 Usage

### Command Line Flags

```
reforge.exe [flags] <directory>
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `reforge.json` | Path to config file |
| `--dry-run` | `false` | Preview mode, no actual conversion |
| `--encoder` | `auto` | Encoder: `auto`, `hevc_nvenc`, `hevc_amf`, `hevc_qsv`, `libx265` |
| `--bypass` | `false` | Re-convert files already recorded in the cache database |
| `--force-hevc` | `false` | Re-compress files that are already H.265/HEVC |
| `--same-drive` | `false` | Write output to the same drive as source (skips drive prompt) |
| `--jobs` | `0` | Parallel conversion jobs; `0` uses benchmark recommendation |
| `--non-interactive` | `false` | Disable interactive prompts (auto-fallback to CPU on GPU failure) |
| `--rebenchmark` | `false` | Force GPU benchmark even if cached results exist |
| `--db-path` | `conversions.db` (next to exe) | Path to SQLite conversion database |

### Interactive Menu

Running `reforge.exe` with no arguments opens an interactive launcher menu instead of starting a conversion. From the menu you can:

- **Start conversion** — fall through to the normal conversion flow
- **Recent runs** — open the two-pane runs browser (Tab to switch pane, PgUp/PgDn to scroll, Esc to return)
- **Stats / Errors / Recent / Not beneficial / Formats / Space saved / Dashboard** — same output as the subcommands below, rendered into a scrollable viewport
- **Quit**

The menu loops until you pick *Start conversion* or *Quit*, so you can browse multiple reports without re-launching the binary. Passing any CLI argument (a path, `--flag`, or a subcommand name) bypasses the menu.

### Subcommands

Query the conversion database without running a conversion:

```bash
reforge.exe stats                        # Overall conversion statistics
reforge.exe errors                       # List failed conversions
reforge.exe recent                       # Show 10 most recent conversions
reforge.exe recent --limit 25            # Show 25 most recent
reforge.exe not-beneficial               # Files where output was larger than input
reforge.exe formats                      # Breakdown by source codec/container
reforge.exe space-saved                  # Total space saved
reforge.exe space-saved --period week    # Space saved in the last week
reforge.exe dashboard                    # Generate interactive HTML dashboard and open in browser
reforge.exe dashboard --no-browser       # Generate without opening
reforge.exe dashboard --output report.html  # Custom output path
```

All subcommands accept `--db-path` to specify a custom database location. The `stats`, `errors`, `not-beneficial`, and `formats` subcommands also accept `--drive` to filter by drive root (e.g. `--drive D:\`).

The `dashboard` subcommand generates a self-contained HTML file with interactive ECharts visualizations: KPI cards, space savings donut chart, conversion timeline, format breakdown, sortable recent conversions table, error list, and not-beneficial list — all filterable by drive via an in-page dropdown.

### Basic Usage
```bash
# Convert all videos in a directory (auto-detects best encoder)
reforge.exe D:\Videos\

# Dry run (preview without converting)
reforge.exe --dry-run D:\Videos\

# Use custom config
reforge.exe --config myconfig.json D:\Videos\

# Force a specific encoder
reforge.exe --encoder libx265 D:\Videos\

# Re-convert everything, even already-cached or already-HEVC files
reforge.exe --bypass --force-hevc D:\Videos\

# Use 4 parallel jobs and write output to the same drive (no prompts)
reforge.exe --jobs 4 --same-drive D:\Videos\

# Non-interactive mode (good for scripts/scheduled tasks)
reforge.exe --non-interactive D:\Videos\

# Re-run GPU benchmark
reforge.exe --rebenchmark D:\Videos\
```

### Interactive Prompts
When you run the tool, it will ask:

1. **Force re-conversion (bypass DB check)?** [y/N]
   - `N` (default): Skip files already processed (cached)
   - `Y`: Re-process everything, ignoring cache

2. **Test re-compression even if file is already HEVC?** [y/N]
   - `N` (default): Skip files already in HEVC format
   - `Y`: Re-encode HEVC files anyway

3. **Output drive** (optional):
   - Press Enter: Output to same drive as source (`D:\REFORGED\`)
   - Specify drive: Output to different drive (`E:\` → `E:\REFORGED\`)

When a GPU encoder fails mid-conversion, the tool asks whether to retry with CPU encoding. Use `--non-interactive` to skip this prompt and auto-fallback.

## ⚙️ Configuration

The tool auto-creates `reforge.json` on first run. Here is the full set of available fields:

```json
{
  "video_encoder":       "auto",
  "quality_preset":      "balanced",
  "max_queue_size":      3,
  "max_parallel_jobs":   1,
  "use_partial_hash":    true,
  "log_level":           "INFO",
  "ffmpeg_path":         "",
  "ffprobe_path":        "",
  "temp_directory":      "",
  "file_extensions":     [".MOV", ".AVI", ".MKV", ".MP4", ".WMV", ".M4V", ".FLV", ".F4V", ".MPG", ".ASF", ".TS", ".M2TS", ".VID"],

  "seq": {
    "enabled":    false,
    "server_url": "http://localhost:5341/",
    "api_key":    ""
  }
}
```

### Field Reference

| Field | Default | Description |
|-------|---------|-------------|
| `video_encoder` | `"auto"` | Encoder to use: `auto`, `hevc_nvenc`, `hevc_amf`, `hevc_qsv`, `libx265` |
| `quality_preset` | `"balanced"` | Encoding quality: `high_quality`, `balanced`, `space_saver` |
| `max_queue_size` | `3` | Job queue buffer size |
| `max_parallel_jobs` | `1` | Concurrent conversions (0 = use benchmark recommendation) |
| `use_partial_hash` | `true` | Fast file identification using a partial BLAKE3 hash |
| `log_level` | `"INFO"` | Log verbosity: `DEBUG`, `INFO`, `WARN`, `ERROR` |
| `ffmpeg_path` | `""` | Path to `ffmpeg.exe`; leave empty to resolve from PATH |
| `ffprobe_path` | `""` | Path to `ffprobe.exe`; leave empty to resolve from PATH |
| `temp_directory` | `""` | Override temp file location; defaults to the output drive |
| `file_extensions` | (13 formats) | List of input extensions to scan (case-insensitive) |
| **`seq.enabled`** | `false` | Enable Seq structured log forwarding |
| `seq.server_url` | `"http://localhost:5341/"` | Seq server base URL |
| `seq.api_key` | `""` | Seq API key (leave empty if Seq is running without authentication) |

### Seq Logging

[Seq](https://datalust.co/seq) is an optional structured log server. When enabled, every log event is forwarded to Seq over HTTP in addition to being written to the local log file.

To enable it:
```json
{
  "seq": {
    "enabled":    true,
    "server_url": "http://localhost:5341/",
    "api_key":    "your-api-key-here"
  }
}
```

- `seq.enabled: false` (the default) — Seq is completely disabled regardless of `server_url` / `api_key`
- If the Seq server is unreachable, the hook disables itself after 5 consecutive failures and logs a warning to stderr; conversions continue normally

### Config Migration

The tool automatically migrates existing config files on load — no manual steps required:

| What changed | Migration behaviour |
|---|---|
| Seq fields moved from flat (`server_url`, `api_key`, `seq_enabled`) to nested `seq` object | Old flat values are promoted into `seq` and removed from the top level |
| New extension added to the canonical list (e.g. `.VID`) | Missing extension is appended to your existing list; custom entries are preserved |

The migrated file is rewritten atomically (write to `.tmp` then rename) so a crash mid-write cannot corrupt your config. If the write fails the tool continues with the migrated values in memory and prints a warning to stderr.
### Quality Presets

| Preset | Use When | Size Reduction | Quality |
|--------|----------|----------------|---------|
| `high_quality` | Archival, max quality | May increase | Excellent |
| `balanced` | General use (default) | 20-40% | Very Good |
| `space_saver` | Already compressed videos | 40-60% | Good |

**Tip:** If files are getting larger, change to `"space_saver"` preset.

See [QUALITY_SETTINGS.md](docs/QUALITY_SETTINGS.md) for detailed quality configuration, including per-encoder quality parameters.

## 📁 Output Structure

The tool preserves directory structure **from the dropped folder onwards**:

**Example:**
```
Input:  Drop C:\Temp\Movies on the exe
        File: C:\Temp\Movies\Action\2024\video.mkv

Output: D:\REFORGED\Movies\Action\2024\video.mp4
```

**Key behaviors:**
- Base directory name is preserved (`Movies` in example above)
- Subdirectory structure is maintained
- **Directories only created if conversion succeeds** (no empty folders)
- Failed/discarded conversions don't create output directories

### Output Formats
- **MP4** → `.mp4` (default for most formats)
- **MKV** → `.mkv` (preserves all audio/subtitle streams & metadata)

## 📚 Documentation

- **[QUICK_START.md](docs/QUICK_START.md)** - Getting started guide with tips
- **[QUALITY_SETTINGS.md](docs/QUALITY_SETTINGS.md)** - Complete quality configuration guide
- **[CHANGELOG.md](docs/CHANGELOG.md)** - Version history and improvements
- **[AGENTS.md](AGENTS.md)** - Developer guide and coding standards

## 💡 Examples

### Scenario 1: Space-Saving Conversion
You have a collection of videos taking up too much space:

```bash
reforge.exe E:\Movies\
# Choose preset "space_saver" in config for maximum compression
# Expected: 40-60% size reduction
```

### Scenario 2: Archive Quality Conversion
Converting high-quality source files for archival:

```bash
reforge.exe --encoder libx265 D:\Archive\
# Use preset "high_quality" in config
# Prioritizes quality over file size
```

### Scenario 3: Re-encode Everything
Force re-conversion of all files including cached and HEVC:

```bash
reforge.exe D:\Videos\
# Answer "y" to both prompts
# Bypasses cache and re-encodes HEVC files
```

### Scenario 4: Output to Different Drive
Convert videos but save to a different drive:

```bash
reforge.exe C:\Videos\
# When prompted for output drive, enter: E:\
# Output will be: E:\REFORGED\Videos\...
```

### Scenario 5: Automated/Scripted Conversion
Run from a script without any interactive prompts:

```bash
reforge.exe --non-interactive --encoder auto D:\Videos\
# GPU auto-detection with automatic CPU fallback on failure
# No user interaction needed
```

## 🔧 Troubleshooting

### Files are getting LARGER after conversion
**Problem:** Quality settings too high for already-compressed videos.

**Solution:**
```json
{
  "quality_preset": "space_saver"
}
```

### All conversions are discarded
**Problem:** Videos may already be optimally compressed.

**Solution:** 
- This is normal. Original files are already efficient.
- Use `--bypass --force-hevc` flags to re-encode anyway (may not improve size)

### FFmpeg not found
**Problem:** FFmpeg executable not in expected location.

**Solution:**
```json
{
  "ffmpeg_path": "C:\\path\\to\\ffmpeg.exe"
}
```

### MediaInfo errors
**Problem:** MediaInfo CLI not found or incorrect path.

**Solution:**
```json
{
  "mediainfo_path": "C:\\path\\to\\MediaInfo.exe"
}
```

### GPU encoding fails
**Problem:** GPU encoder not available or errors during encoding.

**Solution:** The tool auto-falls back to CPU when `--encoder auto` is set. To force CPU encoding:
```bash
reforge.exe --encoder libx265 D:\Videos\
```
Or in config:
```json
{
  "video_encoder": "libx265"
}
```

### FFmpeg 9: NVENC "sdk version not supported" error
**Problem:** When using FFmpeg 9 with an older NVIDIA driver, you may see:
```
[hevc_nvenc] nvenc sdk version 10.0 is not supported
```
or
```
Driver does not support the required nvenc API version.
```

**Cause:** FFmpeg 9 dropped support for NVENC SDK versions older than 11.1 (requires NVIDIA driver ≥ 570).

**Solution:**
1. Update your NVIDIA driver to version **570 or newer** from the [NVIDIA driver download page](https://www.nvidia.com/Download/index.aspx).
2. Or fall back to CPU encoding: `reforge.exe --encoder libx265 D:\Videos\`

Reforge detects this error automatically and will offer the CPU fallback interactively.

### GPU benchmark is slow or stale
**Problem:** First run takes extra time for benchmarking, or cached benchmark results are outdated.

**Solution:** Benchmark results are cached after the first run, so subsequent launches are fast. To force a fresh benchmark:
```bash
reforge.exe --rebenchmark D:\Videos\
```

### Multi-GPU not distributing work
**Problem:** Only one GPU is being used.

**Solution:** Multi-GPU distribution only works with NVIDIA GPUs. AMD and Intel encoders don't support device selection through FFmpeg. Make sure you have multiple NVIDIA GPUs with up-to-date drivers.

### WSL: GPU encoding fails or falls back to CPU
**Problem:** On WSL2, the tool logs `Configured encoder "hevc_nvenc" is not available on this system` and falls back to `libx265`.

**Solution:** GPU encoding on WSL2 requires the [NVIDIA CUDA on WSL](https://docs.nvidia.com/cuda/wsl-user-guide/) driver installed on the **Windows host** (not inside WSL). Verify with:
```bash
nvidia-smi   # Should show your GPU inside WSL
```
If `nvidia-smi` doesn't work, install the latest NVIDIA Game Ready or Studio driver on Windows. The CUDA driver is forwarded into WSL2 automatically. AMD and Intel GPU encoding are not available on WSL.

## 📊 Conversion Statistics

After processing, you'll see a comprehensive summary:

```
╔════════════════════════════════════════════════════════════════╗
║           VIDEO CONVERSION SUMMARY                             ║
╠════════════════════════════════════════════════════════════════╣
║ Files Analyzed:     25                                         ║
║ Files Converted:    12                                         ║
║   → Improved:       8                                          ║
║   → Discarded:      4                                          ║
║ Files Skipped:      11                                         ║
║ Files Errored:      2                                          ║
╠════════════════════════════════════════════════════════════════╣
║ Original Size:      2.5 GB                                     ║
║ Final Size:         1.8 GB                                     ║
║ Space Saved:        700.0 MB (28.0%)                           ║
╚════════════════════════════════════════════════════════════════╝
```

## 🏗️ Architecture

- **GPU Auto-Detection** - Trial-encode verification ensures encoder actually works before use
- **Encoder-Specific Quality** - Each encoder gets its own quality normalization (CRF, CQ, QP, global_quality)
- **Fallback Manager** - GPU error recovery with interactive or automatic CPU fallback
- **Producer-Consumer Pipeline** - Concurrent file processing with configurable queue size
- **BLAKE3 Hashing** - Fast file identification (partial or full hash)
- **SQLite Database** - Centralized conversion cache (`conversions.db` next to executable) with WAL mode
- **Atomic File Operations** - Safe writes with temp files and rename
- **Enriched Metadata** - Source codec, container, resolution, duration, timestamps stored per conversion
- **Query Subcommands** - Seven built-in commands to inspect conversion history, including an interactive HTML dashboard
- **Smart Progress Reporting** - FFmpeg progress parsing via stdout
- **Multi-GPU Distribution** - Speed-balanced work distribution across NVIDIA GPUs

## 🛠️ Developer Workflow

### Makefile Targets

| Target | Description |
|--------|-------------|
| `make` / `make all` | Lint + test + build (auto-detects OS) |
| `make build` | Development build for current OS |
| `make release` | Production build (stripped symbols, smaller binary) |
| `make test` | Run all tests (auto-downloads sample video if missing) |
| `make test-short` | Fast unit tests only (skip integration tests) |
| `make test-cover` | Tests with per-package coverage summary |
| `make cover-html` | Generate and open HTML coverage report |
| `make test-race` | Tests with Go race detector |
| `make lint` | Format (`go fmt`) + static analysis (`go vet`) |
| `make tidy` | Tidy and verify Go module dependencies |
| `make download-sample` | Download Big Buck Bunny sample video (interactive menu) |
| `make clean` | Remove build artifacts and coverage files |

### Sample Videos for Testing

The test suite uses Big Buck Bunny sample clips. Run `make download-sample` for an interactive menu offering:
- **10-second clips** at 360p, 720p, or 1080p (~5 MB each, recommended for tests)
- **Full movie** at 480p, 720p, or 1080p

`make test` automatically checks for sample files and prompts to download if missing (interactive terminals only).

## 🤝 Contributing

This is a personal project, but suggestions and bug reports are welcome! Please check existing documentation before opening issues.

## 📝 License

[Specify your license here - e.g., MIT, GPL-3.0, etc.]

## 🙏 Acknowledgments

- Built with [Go](https://golang.org/)
- Uses [FFmpeg](https://ffmpeg.org/) for video encoding
- Uses [MediaInfo](https://mediaarea.net/) for video analysis
- BLAKE3 hashing via [zeebo/blake3](https://github.com/zeebo/blake3)
- SQLite via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO)
- Logging via [logrus](https://github.com/sirupsen/logrus)

---

**Made with ❤️ for efficient video storage**
