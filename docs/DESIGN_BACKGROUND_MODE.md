# Design: Background Mode ("low tempo" conversion for very large libraries)

Status: **draft for discussion** — nothing in this document is implemented yet.

## 1. Goal

Reforge today converts a batch of files in one interactive session. This design adds a
**background mode** that:

- works through **very large libraries (20–100 TB, hundreds of thousands of files)** over days
  or weeks, resuming by itself after reboots, crashes or pauses;
- keeps the machine **usable**: one encode at a time, low OS priority, automatic pause when the
  machine is busy;
- **actually frees disk space**, by replacing originals with the HEVC file after verification,
  keeping originals in a **quarantine for N days** before they are purged;
- can run **on a Synology NAS** (DS918+, Intel Quick Sync) as well as on the Windows PC (NVENC),
  so the PC does not have to do the work.

Non-goals (for now): re-encoding to codecs other than HEVC, distributed segment-level encoding,
a web UI beyond the existing dashboard.

## 2. Decisions taken so far

| Topic | Decision |
|---|---|
| Scale | 20–100 TB → persistent queue, incremental scan, background probing |
| Concurrency | **One** encode at a time, low OS priority |
| Busy machine | Automatic pause/resume |
| Ordering | **Biggest expected gain first** (saved bytes per encoding hour) |
| Originals | **Quarantine for N days**, then purge |
| Verification | Duration + streams match, **full decode without errors**, **minimum gain** |
| Hardware | NAS and/or PC — to be settled with the benchmark below; design supports both |
| Quality settings | **Profiles per encoder**, calibrated by a quality test (SSIM), never hard-coded |

## 3. Benchmark on the target NAS (measured)

Synology DS918+ (Celeron J3455, 8 GB RAM, DSM 7.3), `linuxserver/ffmpeg` in Docker with
`--device /dev/dri`. One 30 s sample: H.264 1080p, ~6 Mb/s (23.7 MB).

| Encoder | Speed | Output | Saved | SSIM (all) |
|---|---|---|---|---|
| `hevc_vaapi` (qp 26) | 1.83× realtime | 8.93 MB | 62 % | 0.9765 |
| `hevc_qsv` (gq 26) | 1.31× | 7.77 MB | 67 % | 0.9775 |
| `libx265` (medium, crf 26) | 0.24× | 6.80 MB | 71 % | 0.9824 |

Reading:

- Intel hardware encoding works on the NAS; software x265 is unusable at scale (7.5× slower).
- At nominal "26" settings QSV is smaller **and** slightly better than VAAPI, but 28 % slower.
  VAAPI frees ≈30 % more bytes per encoding hour. The settings are not iso-quality, so **no
  winner is declared**: profiles must be calibrated (see §8, `calibrate`).
- Extrapolation (6 Mb/s ≈ 2.7 GB of source per video hour): QSV ≈ 85 GB of source processed per
  day (≈ 57 GB freed); VAAPI ≈ 120 GB/day (≈ 74 GB freed). **The NAS alone needs roughly 6–8 months
  for 20 TB.** It cannot cover the whole library, so prioritisation (§5) is essential, and the PC
  (NVENC) is a useful complement.
- One sample only. Re-run on animation, dark/grainy content and 4K before fixing defaults.
- Measured with software H.264 decoding; hardware decoding (`-hwaccel qsv`) is still to be
  measured and should lower NAS CPU load.

## 4. Architecture overview

```
 scanner ──► index/queue (SQLite) ──► scheduler ──► worker (1 job) ──► verifier ──► swapper
    ▲                ▲                     ▲              │                │           │
    │                │                     │              ▼                ▼           ▼
 stat/ffprobe     gain model        throttle controller   temp file     checks     quarantine ─► purge
 (low priority)   (learns)          (schedule, idle, load)
```

Everything except the throttle controller's OS probes and the encoder arguments is platform
independent and reuses existing packages: `converter` (FFmpeg orchestration), `ffmpeg`
(progress, suspend/resume), `encoder` (per-encoder arguments), `database` (SQLite via pure-Go
`modernc.org/sqlite` — no CGO, so a static Linux binary for the NAS is a plain cross-compile),
`pipeline.Controller` (pause/stop), `tui`/`dashboard` (reporting).

New package: `internal/background` (queue, scanner, estimator, scheduler, throttle, verify,
quarantine). New subcommand group: `reforge background …` (§8).

## 5. Queue, scanning and gain estimation

### 5.1 Tables (same SQLite file as `conversions`)

`queue` — one row per candidate file:

| Column | Notes |
|---|---|
| `id`, `root`, `path`, `size`, `mtime` | from the stat-only scan |
| `state` | `discovered → probed → queued → encoding → verifying → swapping → quarantined → purged`, plus `skipped`, `failed` |
| `codec`, `width`, `height`, `bitrate`, `duration`, `audio/sub counts` | filled by the probe phase |
| `est_saved_bytes`, `est_encode_secs`, `score` | gain model output |
| `attempts`, `last_error`, `lease_owner`, `lease_until` | retries and (later) multi-worker leases |
| `quarantine_path`, `purge_after` | set when the original is quarantined |

Existing `conversions`/`runs`/`run_files` stay the history and the "already processed" cache.

### 5.2 Two-phase scan (cheap first)

Probing 300k files with `ffprobe` over SMB can take tens of hours, so:

1. **Enumerate** (stat only: size, mtime). Skips: `REFORGED`, the quarantine folder, and on
   Synology `@eaDir`, `#recycle`, `#snapshot` (generated thumbnails/previews would otherwise be
   "converted"). Files modified in the last *N* hours (still being written/downloaded) are deferred.
2. **Probe lazily in priority order**: biggest files first (a good proxy for gain), at low priority,
   while encoding already runs. The ordering sharpens as probes complete.

Re-scans are incremental: unchanged `(path, size, mtime)` rows are not re-probed.

### 5.3 Gain model

```
score = est_saved_bytes / est_encode_secs
est_saved_bytes = size × (1 − expected_ratio[codec, resolution bucket])
est_encode_secs = duration / speed[engine, resolution bucket]
```

Bootstrapped from the benchmark (H.264 1080p: ratio ≈ 0.33 for QSV, speed ≈ 1.3×) and then
**learned** from completed jobs (moving average per bucket, stored in a `stats` table). Saved
bytes per hour scales with bitrate while encode time depends mostly on resolution, so
high-bitrate sources (old codecs, 4K) naturally rise to the top. Files already HEVC/AV1 are
`skipped`. A file whose estimated ratio is below the minimum gain is `skipped` too.

## 6. Throttling

A single **Throttle Controller** decides `Running` or `Paused(reason)`, with hysteresis
(default: busy ≥ 10 s → pause, free ≥ 60 s → resume).

Inputs (each optional, configurable):

- **Schedule window** (e.g. nights only).
- **User activity / full-screen app** (Windows: `GetLastInputInfo`, `SHQueryUserNotificationState`).
- **CPU load and GPU utilisation** (`nvidia-smi` — `internal/gpu/nvidia` already models GPU/encoder
  utilisation fields; Linux load average on the NAS).
- **NAS-specific**: SMB/NFS activity or load average, so media streaming stays smooth.

Actions:

- **Short pause** (< `max_suspend`, default 10 min): suspend the FFmpeg process (existing
  `NtSuspendProcess` / `SIGSTOP` mechanism, already fixed to resume correctly on stop).
- **Long pause**: abort the job and requeue it, to release GPU memory and the NVENC session.
  Progress is lost; acceptable for typical file lengths. Segment-level resume is a non-goal.

Process priority: FFmpeg is started **below normal / idle** (Windows `CreationFlags`, I/O priority
via `NtSetInformationProcess` — to validate; Linux `nice 19` + `ionice -c3`, and Docker CPU limits
on the NAS). One job at a time, optional pause between files ("low tempo").

## 7. Verify, swap, quarantine, purge

Per job, with the state persisted **before** each step so a crash can always be reconciled:

1. **Encode** to a temp file **on the same volume as the source** (atomic rename later, no copy).
2. **Verify** (all must pass, else the temp is discarded and the original untouched):
   - `ffprobe` duration within `max(2 s, 0.5 %)` of the source;
   - audio/subtitle stream counts match what the command line mapped; **if the target container
     would drop streams (e.g. image subtitles in MP4) the job is refused or switched to MKV** — never
     silently lose tracks when originals are going to be deleted;
   - **full decode** `ffmpeg -v error -i out -f null -` with no errors (hardware-accelerated where
     possible; budget ≈ 20–40 % of encode time on the NAS);
   - **minimum gain** (default 10 %), otherwise discard (`not_beneficial`, as today).
3. **Swap**: rename original → `<volume>/.reforge-quarantine/<date>/<relative path>`, then rename the
   verified temp → original location. Same directory and base name; the extension follows the
   container (so `a.avi` becomes `a.mp4` — media-library apps will see a new file; documented).
   Copy mode, owner/group/ACL and original `mtime` to the new file (important on Synology).
4. **Purge** after N days (`purge_after`), by a janitor task. `quarantine restore <path>` reverses a swap.
5. **Crash recovery** on startup: `encoding/verifying` → delete temp, requeue; `swapping` → reconcile
   from what exists on disk (original, temp, quarantine copy).

**Space accounting:** report real free space per volume, not the sum of file sizes. On Btrfs with
snapshots or Hyper Backup, deleting/purging an original frees nothing until the snapshots expire;
the report must say so.

## 8. Commands and configuration

```
reforge background run [roots…]      # start/resume the worker (foreground; service/container friendly)
reforge background status            # state, current job, queue size, estimated GB freed/day
reforge background pause|resume      # manual override via a local control endpoint
reforge background scan [roots…]     # (re)enumerate and probe
reforge background quarantine list|restore|purge
reforge background calibrate <file>  # sweep quality settings, report size/speed/SSIM per profile
```

Config (extends `reforge.json`, existing style):

```json
"background": {
  "roots": ["/volume1/Videos"],
  "profile": "qsv-balanced",
  "min_gain_percent": 10,
  "quarantine_days": 14,
  "min_file_age_hours": 24,
  "pause_between_files_secs": 0,
  "schedule": [],
  "pause_when": { "user_active": true, "fullscreen_app": true, "cpu_percent_above": 70, "gpu_percent_above": 60 },
  "max_suspend_minutes": 10
}
```

A **profile** = encoder + quality arguments + expected ratio/speed seeds, produced and validated by
`calibrate` (SSIM against the source on a sample; VMAF if available).

## 9. NAS deployment (Synology DS918+)

Docker (Container Manager), image = static `reforge` Linux binary + FFmpeg with Intel media driver
(based on `linuxserver/ffmpeg`, which worked in the benchmark). `--device /dev/dri`, bind mounts
for the volumes, `restart: unless-stopped`, CPU limits (`--cpus`, `--cpu-shares`) to keep DSM
responsive. The SQLite file lives on a local volume.

Items to validate or fix for Linux (found while reading the code):

- **`killProcess` on Unix kills the process group (`kill(-pid)`) but FFmpeg is never started with
  `Setpgid`**, so the corrupt-stream kill does nothing on Linux. Set `SysProcAttr{Setpgid: true}`.
- **`QsvEncoder.DeviceArgs` is empty.** The benchmark needed `-init_hw_device qsv=hw`,
  `-filter_hw_device hw` and `-vf hwupload=extra_hw_frames=64,format=qsv`. Check whether plain
  `-c:v hevc_qsv` works in the container; otherwise add Linux-specific device/filter arguments.
- Add a `hevc_vaapi` encoder (`-vaapi_device`, `-vf format=nv12,hwupload`).
- Exclude Synology system folders (§5.2).

## 10. Multiple workers (PC + NAS)

**Do not share one SQLite file over SMB** (locking/WAL are unsafe on network shares). Plan:

- Phase 1: each worker owns its queue for its own roots (the NAS handles NAS volumes).
- Phase 2: the NAS hosts the queue behind a small authenticated HTTP API; workers **claim jobs with
  leases** (`lease_until`, heartbeat), so the PC can take work at night over SMB without two machines
  encoding the same file. A file is verified/swapped by whichever machine encoded it, over the share.

## 11. Milestones

| # | Scope | Done when |
|---|---|---|
| **M1** | Queue + two-phase scanner + gain model + `background run/status` on the PC; one job, low priority, schedule + idle/full-screen pause; **output next to the original as today (no deletion)** | Resumes after kill/reboot; ordering follows score; machine stays usable (manual check); fake-ffmpeg tests for queue transitions and pause/resume |
| **M2** | Verify + swap + quarantine + purge + restore + crash recovery | Every crash point reconciles correctly (tests with injected failures); no stream-dropping swaps; space report |
| **M3** | Linux/NAS: Setpgid fix, QSV/VAAPI arguments, priority/nice, Synology exclusions, Docker image, `status` endpoint | Runs a full cycle on the DS918+ under DSM; measured hardware-decode speed |
| **M4** | `calibrate` + profiles; multi-worker leases (PC + NAS) | Iso-quality profile comparison reproducible; no duplicate work with two workers |

Each milestone ships with tests using the existing fake-`ffmpeg`/`ffprobe` approach
(`internal/converter/process_test.go`, `internal/discovery/produce_test.go`).

## 12. Risks and open questions

- **Irreversibility:** the quarantine is the only safety net; purge must never run before
  `purge_after` and must log every deletion.
- **Btrfs snapshots / backups** may keep originals alive (see §7). Which filesystem and snapshot
  policy does the NAS use?
- **Files in use** (being written, seeded, played): rely on `min_file_age_hours`; consider open-file
  checks on Windows/SMB.
- **Path/extension changes** break references in media libraries (Plex/Jellyfin/Video Station) and
  playlists; optionally keep the original container when it can hold the streams.
- **Quality ≠ SSIM:** SSIM/VMAF on one sample is a guide, not proof. The default profile should be
  conservative, and `calibrate` should be run per content type.
- **Verification cost** on the NAS (full decode) may dominate throughput; measure before making it
  mandatory for every file.
- **Open question:** hardware-decode speed on the NAS (test pending), PC GPU model and NVENC speed,
  and the exact `N` (quarantine days) and minimum gain to use by default.
