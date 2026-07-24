//go:build windows

package app

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ydakilux/reforge/internal/tui"
)

// getDiskFreeBytes returns (availableBytes, totalBytes) for the given path using
// the Windows GetDiskFreeSpaceExW API.
func getDiskFreeBytes(path string) (freeBytes, totalBytes uint64, ok bool) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")
	driveName, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	var avail, total, free uint64
	ret, _, _ := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(driveName)),
		uintptr(unsafe.Pointer(&avail)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&free)),
	)
	if ret == 0 {
		return 0, 0, false
	}
	return avail, total, true
}

// getAvailableDrives returns a list of available Windows drive roots with
// free/total space annotations and raw free bytes for space checks.
func getAvailableDrives() []tui.DriveInfo {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getLogicalDrives := kernel32.NewProc("GetLogicalDrives")
	getDriveType := kernel32.NewProc("GetDriveTypeW")

	ret, _, _ := getLogicalDrives.Call()
	bitmask := uint32(ret)
	if bitmask == 0 {
		// Fallback to checking C:\ if API fails
		bitmask = 1 << 2
	}

	type candidateDrive struct {
		index int
		path  string
	}
	var candidates []candidateDrive

	for i := 0; i < 26; i++ {
		if bitmask&(1<<uint(i)) == 0 {
			continue // Drive letter is not assigned
		}
		driveLetter := string(rune('A' + i))
		drivePath := driveLetter + ":\\"

		drivePtr, err := syscall.UTF16PtrFromString(drivePath)
		if err == nil {
			dtype, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(drivePtr)))
			// 0 = DRIVE_UNKNOWN, 1 = DRIVE_NO_ROOT_DIR, 5 = DRIVE_CDROM (optical read-only)
			if dtype == 0 || dtype == 1 || dtype == 5 {
				continue
			}
		}

		candidates = append(candidates, candidateDrive{index: i, path: drivePath})
	}

	type driveResult struct {
		index int
		info  tui.DriveInfo
	}
	resultsCh := make(chan driveResult, len(candidates))
	var wg sync.WaitGroup

	for _, cand := range candidates {
		wg.Add(1)
		go func(idx int, path string) {
			defer wg.Done()

			type spaceResult struct {
				freeB, totalB uint64
				ok            bool
			}
			spaceCh := make(chan spaceResult, 1)

			go func() {
				f, t, ok := getDiskFreeBytes(path)
				spaceCh <- spaceResult{freeB: f, totalB: t, ok: ok}
			}()

			var label string
			var freeBytes int64

			select {
			case res := <-spaceCh:
				if res.ok {
					label = fmt.Sprintf("%s (%.1f GB free / %.1f GB total)",
						path,
						float64(res.freeB)/(1024*1024*1024),
						float64(res.totalB)/(1024*1024*1024),
					)
					freeBytes = int64(res.freeB)
				} else {
					label = path
				}
			case <-time.After(500 * time.Millisecond):
				// Drive query timed out (e.g. offline SMB network share or sleeping drive)
				label = path
			}

			resultsCh <- driveResult{
				index: idx,
				info:  tui.DriveInfo{Root: path, Label: label, FreeBytes: freeBytes},
			}
		}(cand.index, cand.path)
	}

	wg.Wait()
	close(resultsCh)

	resultMap := make(map[int]tui.DriveInfo, len(candidates))
	for res := range resultsCh {
		resultMap[res.index] = res.info
	}

	var drives []tui.DriveInfo
	for _, cand := range candidates {
		if info, ok := resultMap[cand.index]; ok {
			drives = append(drives, info)
		}
	}

	return drives
}
