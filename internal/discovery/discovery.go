// Package discovery walks input paths and produces conversion Jobs into the pipeline.
package discovery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	cfgpkg "github.com/ydakilux/reforge/internal/config"
	"github.com/ydakilux/reforge/internal/database"
	"github.com/ydakilux/reforge/internal/ffmpeg"
	"github.com/ydakilux/reforge/internal/fileutil"
	"github.com/ydakilux/reforge/internal/pipeline"
	"github.com/ydakilux/reforge/internal/types"
)

// DiscoverFiles walks paths and returns all matching video files with their
// base directories. The base directory is the user-provided input path.
func DiscoverFiles(paths []string, extensions []string, log *logrus.Logger) ([]string, map[string]string) {
	var files []string
	fileToBaseDir := make(map[string]string)
	extMap := make(map[string]bool)
	for _, ext := range extensions {
		extMap[strings.ToLower(ext)] = true
	}

	for _, p := range paths {
		absPath, err := filepath.Abs(p)
		if err != nil {
			log.Warnf("Failed to get absolute path for %s: %v", p, err)
			continue
		}

		info, err := os.Stat(absPath)
		if err != nil {
			log.Warnf("Failed to stat %s: %v", absPath, err)
			continue
		}

		if info.IsDir() {
			filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error { //nolint:errcheck
				if err != nil {
					return nil
				}
				if !info.IsDir() && extMap[strings.ToLower(filepath.Ext(path))] {
					files = append(files, path)
					fileToBaseDir[path] = absPath
				}
				return nil
			})
		} else {
			if extMap[strings.ToLower(filepath.Ext(absPath))] {
				files = append(files, absPath)
				fileToBaseDir[absPath] = filepath.Dir(absPath)
			}
		}
	}

	return files, fileToBaseDir
}

// ProducerConfig holds everything the producer needs to analyse files and
// enqueue jobs — all values are read-only during the run.
type ProducerConfig struct {
	Config      *types.Config
	ExecDir     string
	DB          database.Store
	Stats       *types.Stats
	FFprobePath string // resolved ffprobe path (may be empty → resolves from ffmpeg dir)
	Log         *logrus.Logger
	GPUAssigner *pipeline.GPUAssigner
	// RunID, when non-zero, is stamped onto every conversion record this
	// producer writes (currently only the "already_hevc" prescan rows).
	// Leaving it zero preserves the pre-runs behaviour of writing run_id
	// NULL — but in that case the next launch's migration will synthesise a
	// "legacy" run for these rows, so callers should always pass the
	// current run id when one exists.
	RunID int64
	// OnFileFinished is called whenever a file is fully handled (converted,
	// skipped, or errored) so external callers (e.g. the TUI) can update
	// progress. sizeBytes is the original file size (0 if unknown). May be nil.
	OnFileFinished func(sizeBytes int64)
}

// Produce analyses files, filters already-processed ones, and submits Jobs to
// the pipeline. It calls pipe.Wait() when done. Run in a goroutine.
func Produce(files []string, fileToBaseDir map[string]string, pipe *pipeline.Pipeline, bypass, forceHevc bool, cfg ProducerConfig) {
	defer pipe.Wait()

	notifyFinished := func(size int64) {
		if cfg.OnFileFinished != nil {
			cfg.OnFileFinished(size)
		}
	}

	ffprobeExe := resolveFFprobeExe(cfg.Config, cfg.ExecDir)

	// Group files by parent folder for nicer log ordering
	folderMap := make(map[string][]string)
	for _, filePath := range files {
		driveRoot := fileutil.GetDriveRoot(filePath)
		parentFolder := fileutil.GetParentFolderName(filePath, driveRoot)
		folderMap[parentFolder] = append(folderMap[parentFolder], filePath)
	}

	var folders []string
	for folder := range folderMap {
		folders = append(folders, folder)
	}
	sort.Strings(folders)

	totalFolders := len(folders)
	totalFiles := len(files)
	globalFileNumber := 0

	for folderIdx, folder := range folders {
		folderNumber := folderIdx + 1
		filesInFolder := folderMap[folder]

		cfg.Log.Infof("\nProcessing folder %d/%d: %s (%d files)", folderNumber, totalFolders, folder, len(filesInFolder))

		for _, filePath := range filesInFolder {
			globalFileNumber++
			cfg.Log.Debugf("[%d/%d] Analyzing: %s", globalFileNumber, totalFiles, filepath.Base(filePath))

			cfg.Stats.IncrFilesAnalyzed()

			info, err := os.Stat(filePath)
			if err != nil {
				cfg.Log.Warnf("Failed to stat %s: %v", filePath, err)
				cfg.Stats.IncrFilesErrored()
				notifyFinished(0)
				continue
			}

			driveRoot := fileutil.GetDriveRoot(filePath)
			cfg.Stats.AddTouchedDrive(driveRoot)

			fileHash, err := fileutil.GetFileHash(filePath, cfg.Config.UsePartialHash)
			if err != nil {
				cfg.Log.Warnf("Failed to hash %s: %v", filePath, err)
				cfg.Stats.IncrFilesErrored()
				notifyFinished(info.Size())
				continue
			}

			if !bypass {
				rec, err := cfg.DB.GetRecord(context.Background(), driveRoot, fileHash)
				if err != nil {
					cfg.Log.Warnf("Failed to check DB for %s: %v", filePath, err)
				}
				if rec != nil && (rec.Output != "" || rec.Note == "not_beneficial" || rec.Note == "already_hevc") {
					cfg.Log.Debugf("Skipping %s (already processed)", filePath)
					cfg.Stats.IncrFilesSkipped()
					notifyFinished(info.Size())
					continue
				}
			}

			videoInfo, err := ffmpeg.GetMediaInfo(filePath, ffprobeExe)
			if err != nil {
				cfg.Log.Errorf("Failed to get video info for %s: %v", filePath, err)
				if cfg.DB != nil {
					if dbErr := cfg.DB.UpdateRecord(context.Background(), driveRoot, fileHash, types.Record{
						OriginalSize: info.Size(),
						Error:        "probe_failed",
						SourcePath:   filePath,
						ConvertedAt:  time.Now().UTC().Format(time.RFC3339),
						RunID:        cfg.RunID,
					}); dbErr != nil {
						cfg.Log.Errorf("Failed to update error record for %s: %v", filePath, dbErr)
					}
				}
				cfg.Stats.IncrFilesErrored()
				notifyFinished(info.Size())
				continue
			}
			if videoInfo == nil {
				cfg.Log.Errorf("No video track found in %s", filePath)
				if cfg.DB != nil {
					if dbErr := cfg.DB.UpdateRecord(context.Background(), driveRoot, fileHash, types.Record{
						OriginalSize: info.Size(),
						Error:        "no_video_track",
						SourcePath:   filePath,
						ConvertedAt:  time.Now().UTC().Format(time.RFC3339),
						RunID:        cfg.RunID,
					}); dbErr != nil {
						cfg.Log.Errorf("Failed to update error record for %s: %v", filePath, dbErr)
					}
				}
				cfg.Stats.IncrFilesErrored()
				notifyFinished(info.Size())
				continue
			}

			if ffmpeg.IsHEVC(videoInfo.Format, videoInfo.CodecID) && !forceHevc {
				cfg.Log.Infof("Skipping %s (already HEVC)", filePath)
				if err := cfg.DB.UpdateRecord(context.Background(), driveRoot, fileHash, types.Record{
					OriginalSize:    info.Size(),
					ConvertedSize:   info.Size(),
					Note:            "already_hevc",
					SourceCodec:     videoInfo.CodecID,
					SourceContainer: strings.ToLower(filepath.Ext(filePath)),
					SourcePath:      filePath,
					Width:           videoInfo.Width,
					Height:          videoInfo.Height,
					ConvertedAt:     time.Now().UTC().Format(time.RFC3339),
					RunID:           cfg.RunID,
				}); err != nil {
					cfg.Log.Warnf("Failed to update already_hevc record for %s: %v", filePath, err)
				}
				cfg.Stats.IncrFilesSkipped()
				notifyFinished(info.Size())
				continue
			}

			duration := ffmpeg.GetDuration(filePath, ffprobeExe, cfg.Log)

			job := types.Job{
				FilePath:        filePath,
				BaseDir:         fileToBaseDir[filePath],
				DriveRoot:       driveRoot,
				FileHash:        fileHash,
				OriginalSize:    info.Size(),
				Width:           videoInfo.Width,
				Height:          videoInfo.Height,
				Format:          videoInfo.Format,
				CodecID:         videoInfo.CodecID,
				DurationSeconds: duration,
				FileNumber:      globalFileNumber,
				TotalFiles:      totalFiles,
				FolderNumber:    folderNumber,
				TotalFolders:    totalFolders,
				VideoInfo:       videoInfo,
				GPUIndex:        cfg.GPUAssigner.Next(),
			}
			pipe.Submit(job) //nolint:errcheck
		}
	}
}

// resolveFFprobeExe finds the ffprobe executable from config or by locating it
// beside ffmpeg.
func resolveFFprobeExe(cfg *types.Config, execDir string) string {
	ffprobeExe := cfgpkg.ResolveExecutable(cfg.FFprobePath, cfgpkg.ExeName("ffprobe"), execDir)
	if ffprobeExe == "" {
		ffmpegExe := cfgpkg.ResolveExecutable(cfg.FFmpegPath, cfgpkg.ExeName("ffmpeg"), execDir)
		ffprobeExe = filepath.Join(filepath.Dir(ffmpegExe), cfgpkg.ExeName("ffprobe"))
		if _, err := os.Stat(ffprobeExe); err != nil {
			ffprobeExe, _ = exec.LookPath(cfgpkg.ExeName("ffprobe"))
		}
	}
	return ffprobeExe
}
