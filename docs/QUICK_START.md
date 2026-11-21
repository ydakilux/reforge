# Quick Start - Reforge

## ⚠️ Windows Security Note

After downloading `reforge.exe` from GitHub, Windows may block it from running. To fix this:

1. Right-click the downloaded `.exe` file
2. Select **Properties**
3. At the bottom, check the **Unblock** checkbox
4. Click **OK**

This is a standard Windows security measure for files downloaded from the internet. You only need to do this once.

## 🚀 Basic Usage

```bash
# Convert all videos in a folder (interactive prompts guide you through the rest)
reforge.exe D:\Videos\

# Multiple folders
reforge.exe D:\Movies\ E:\Shows\

# On WSL / Linux
./reforge /mnt/d/Videos/
```

> **WSL/Linux**: GPU encoding requires NVIDIA GPU + [CUDA on WSL](https://docs.nvidia.com/cuda/wsl-user-guide/) driver on the Windows host. Without it, the tool falls back to CPU (`libx265`) automatically. AMD/Intel GPU encoding is not available on WSL.
```

## 🔧 Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | off | Preview what would be converted without writing any files |
| `--bypass` | off | Re-convert files already recorded in the database |
| `--force-hevc` | off | Re-compress files that are already H.265/HEVC (overrides an earlier "already HEVC" skip) |
| `--same-drive` | off | Write output to the same drive as the source (skips drive prompt) |
| `--jobs <n>` | `0` (auto) | Number of parallel conversion jobs; `0` uses the benchmark recommendation |
| `--encoder <name>` | `auto` | Force a specific encoder (see below) |
| `--config <path>` | `reforge.json` | Path to config file |
| `--non-interactive` | off | Skip GPU fallback prompts (useful for scripted runs) |
| `--rebenchmark` | off | Force GPU benchmark even if a cached result exists |
| `--db-path <path>` | `conversions.db` (next to exe) | Path to SQLite conversion database |

### Encoder options for `--encoder`

| Value | Description |
|-------|-------------|
| `auto` | Auto-detect best GPU encoder (default) |
| `hevc_nvenc` | NVIDIA GPU (H.265) |
| `hevc_amf` | AMD GPU (H.265) |
| `hevc_qsv` | Intel Quick Sync (H.265) |
| `libx265` | CPU software encoder (slowest, most compatible) |

## 💬 Interactive Prompts

On each run you will be asked:

1. **Force re-conversion?** — Re-process files already in the database (`y/N`)
2. **Test already-HEVC files?** — Re-compress files that are already H.265 (`y/N`)
3. **Parallel jobs** — How many files to encode simultaneously (pre-filled from benchmark)
4. **Output drive** — Write results to a different drive than the source (`y/N`)
5. **Input folder** — Only asked if no path was given on the command line

## 📊 Quality Presets

Set `quality_preset` in `reforge.json`:

| Preset | Best for | Expected savings |
|--------|----------|-----------------|
| `high_quality` | Archival / max quality | Minimal or none |
| `balanced` | General use (default) | 20–40% |
| `space_saver` | Already-compressed sources | 40–60% |

## 🎬 Common Examples

```bash
# Run with exactly 2 parallel jobs (skips the interactive jobs prompt)
reforge.exe --jobs 2 D:\Videos\

# Re-convert everything, even already-processed or already-HEVC files
reforge.exe --bypass --force-hevc D:\Videos\

# Write output to the same drive as the source, no prompts at all
reforge.exe --same-drive --bypass --jobs 4 D:\Videos\

# Dry run — see what would happen without converting anything
reforge.exe --dry-run D:\Videos\

# Force CPU encoder (no GPU required)
reforge.exe --encoder libx265 D:\Videos\

# Use a custom config file
reforge.exe --config D:\my-settings.json D:\Videos\

# Re-run benchmark before starting (after driver update, etc.)
reforge.exe --rebenchmark D:\Videos\

# Fully non-interactive (no GPU fallback prompts; still asks for folder/drive)
reforge.exe --non-interactive D:\Videos\
```

## 📁 Output Location

Converted files are written to:
```
<drive>\REFORGED\<source-folder>\<filename>.mp4
```

Files that are **larger** after conversion are discarded automatically — the original is left untouched.

## 🔍 Query Subcommands

Inspect the conversion database without running a conversion:

```bash
# Overall statistics (total files, space saved, success/error counts)
reforge.exe stats

# Filter stats by drive
reforge.exe stats --drive D:\

# List failed conversions with error messages
reforge.exe errors

# Show 10 most recent conversions (default)
reforge.exe recent

# Show 25 most recent
reforge.exe recent --limit 25

# Files where the converted output was larger than the original
reforge.exe not-beneficial

# Breakdown by source codec and container format
reforge.exe formats

# Total space saved (all time)
reforge.exe space-saved

# Space saved in the last week or month
reforge.exe space-saved --period week
reforge.exe space-saved --period month

# Generate an interactive HTML dashboard and open in browser
reforge.exe dashboard

# Generate without opening, custom output path
reforge.exe dashboard --no-browser --output report.html
```

All subcommands accept `--db-path` to use a custom database location.

## 📈 Benchmark Tool

Find the optimal `--jobs` value for your hardware by running the converter multiple times and comparing elapsed times.

```bash
# Build the benchmark tool (one-time)
go build -o benchmark.exe ./cmd/benchmark

# Run with default jobs list (1,2,4,8) against a sample folder
benchmark.exe --input D:\Videos\

# Test specific jobs values
benchmark.exe --input D:\Videos\ --jobs 1,2,4

# Custom binary path and output file
benchmark.exe --input D:\Videos\ --jobs 2,4 --bin D:\tools\reforge.exe --output my_results.csv
```

### Benchmark flags

| Flag | Default | Description |
|------|---------|-------------|
| `--input <dir>` | *(required)* | Input directory to convert |
| `--jobs <list>` | `1,2,4,8` | Comma-separated list of `--jobs` values to test |
| `--output <file>` | `benchmark_results.csv` | CSV output path |
| `--bin <path>` | auto-detect | Path to `reforge` binary |
| `--extra-flags <str>` | *(none)* | Additional flags forwarded to each converter run |

The tool prints a summary table and writes a CSV with columns `jobs,elapsed,wall_ms,error`:

```
▶  jobs=1 ... ELAPSED=4m 12s  wall=4m 15s
▶  jobs=2 ... ELAPSED=2m 34s  wall=2m 37s
▶  jobs=4 ... ELAPSED=1m 58s  wall=2m 01s
▶  jobs=8 ... ELAPSED=2m 03s  wall=2m 06s

┌────────┬──────────────┬──────────────┐
│  jobs  │   elapsed    │  wall time   │
├────────┼──────────────┼──────────────┤
│ 1      │ 4m 12s       │ 4m 15s       │
│ 2      │ 2m 34s       │ 2m 37s       │
│ 4      │ 1m 58s       │ 2m 01s       │
│ 8      │ 2m 03s       │ 2m 06s       │
└────────┴──────────────┴──────────────┘
```

> **Note:** The benchmark passes `--bypass --force-hevc --same-drive --non-interactive` automatically so each run processes the same files under comparable conditions. The GPU benchmark cache is reused across runs — use `--extra-flags --rebenchmark` only if you want to force a fresh GPU sweep.

## 📖 Further Reading

- **QUALITY_SETTINGS.md** — Complete quality and CRF/CQ guide
- **CHANGELOG.md** — Full change history
- **AGENTS.md** — Developer / contributor guide
