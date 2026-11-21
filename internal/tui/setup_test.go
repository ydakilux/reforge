package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ── togglePath ───────────────────────────────────────────────────────────────

func TestTogglePathAdd(t *testing.T) {
	paths := togglePath(nil, `D:\Videos`)
	if len(paths) != 1 || paths[0] != `D:\Videos` {
		t.Fatalf("togglePath add: got %v, want [D:\\Videos]", paths)
	}
}

func TestTogglePathRemoveDuplicate(t *testing.T) {
	paths := []string{`D:\Videos`, `E:\Movies`}
	paths = togglePath(paths, `D:\Videos`)
	if len(paths) != 1 || paths[0] != `E:\Movies` {
		t.Fatalf("togglePath remove duplicate: got %v, want [E:\\Movies]", paths)
	}
}

func TestTogglePathCaseInsensitive(t *testing.T) {
	paths := []string{`D:\Videos`}
	paths = togglePath(paths, `d:\videos`)
	if len(paths) != 0 {
		t.Fatalf("togglePath case-insensitive remove: got %v, want []", paths)
	}
}

func TestTogglePathReAdd(t *testing.T) {
	paths := togglePath(nil, `D:\Videos`)
	paths = togglePath(paths, `D:\Videos`)
	if len(paths) != 0 {
		t.Fatalf("after toggle off: got %v, want []", paths)
	}
	paths = togglePath(paths, `D:\Videos`)
	if len(paths) != 1 || paths[0] != `D:\Videos` {
		t.Fatalf("after re-add: got %v, want [D:\\Videos]", paths)
	}
}

func TestTogglePathMultiple(t *testing.T) {
	var paths []string
	paths = togglePath(paths, `A:\one`)
	paths = togglePath(paths, `B:\two`)
	paths = togglePath(paths, `C:\three`)
	if len(paths) != 3 {
		t.Fatalf("togglePath multi-add: got %d items, want 3", len(paths))
	}
	paths = togglePath(paths, `B:\two`)
	if len(paths) != 2 || paths[0] != `A:\one` || paths[1] != `C:\three` {
		t.Fatalf("togglePath remove middle: got %v", paths)
	}
}

// ── nextStepAfter ────────────────────────────────────────────────────────────

func TestNextStepAfterAllNeeded(t *testing.T) {
	m := setupModel{opts: SetupOptions{
		NeedFolder:       true,
		NeedBypass:       true,
		NeedForceHevc:    true,
		NeedParallelJobs: true,
		NeedOutputDrive:  true,
	}}

	if got := m.nextStepAfter(stepStartup); got != stepFolder {
		t.Errorf("after stepStartup: got %d, want stepFolder(%d)", got, stepFolder)
	}
	if got := m.nextStepAfter(stepFolder); got != stepConfirm {
		t.Errorf("after stepFolder: got %d, want stepConfirm(%d)", got, stepConfirm)
	}
	if got := m.nextStepAfter(stepConfirm); got != stepBypass {
		t.Errorf("after stepConfirm: got %d, want stepBypass(%d)", got, stepBypass)
	}
	if got := m.nextStepAfter(stepBypass); got != stepForceHevc {
		t.Errorf("after stepBypass: got %d, want stepForceHevc(%d)", got, stepForceHevc)
	}
	if got := m.nextStepAfter(stepForceHevc); got != stepParallelJobs {
		t.Errorf("after stepForceHevc: got %d, want stepParallelJobs(%d)", got, stepParallelJobs)
	}
	if got := m.nextStepAfter(stepParallelJobs); got != stepOutputDrive {
		t.Errorf("after stepParallelJobs: got %d, want stepOutputDrive(%d)", got, stepOutputDrive)
	}
	if got := m.nextStepAfter(stepOutputDrive); got != stepDone {
		t.Errorf("after stepOutputDrive: got %d, want stepDone(%d)", got, stepDone)
	}
}

func TestNextStepAfterNoneNeeded(t *testing.T) {
	m := setupModel{opts: SetupOptions{}}
	for _, from := range []setupStep{stepStartup, stepFolder, stepConfirm, stepBypass, stepForceHevc, stepParallelJobs, stepOutputDrive} {
		if got := m.nextStepAfter(from); got != stepDone {
			t.Errorf("nextStepAfter(%d) with no flags: got %d, want stepDone(%d)", from, got, stepDone)
		}
	}
}

func TestNextStepAfterSkipsBypass(t *testing.T) {
	m := setupModel{opts: SetupOptions{
		NeedBypass:    false,
		NeedForceHevc: true,
	}}
	if got := m.nextStepAfter(stepConfirm); got != stepForceHevc {
		t.Errorf("skip bypass: got %d, want stepForceHevc(%d)", got, stepForceHevc)
	}
}

func TestNextStepAfterSkipsForceHevc(t *testing.T) {
	m := setupModel{opts: SetupOptions{
		NeedBypass:       true,
		NeedForceHevc:    false,
		NeedParallelJobs: true,
	}}
	if got := m.nextStepAfter(stepBypass); got != stepParallelJobs {
		t.Errorf("skip forceHevc: got %d, want stepParallelJobs(%d)", got, stepParallelJobs)
	}
}

func TestNextStepAfterSkipsParallelJobs(t *testing.T) {
	m := setupModel{opts: SetupOptions{
		NeedForceHevc:    true,
		NeedParallelJobs: false,
		NeedOutputDrive:  true,
	}}
	if got := m.nextStepAfter(stepForceHevc); got != stepOutputDrive {
		t.Errorf("skip parallelJobs: got %d, want stepOutputDrive(%d)", got, stepOutputDrive)
	}
}

func TestNextStepAfterOnlyOutputDrive(t *testing.T) {
	m := setupModel{opts: SetupOptions{
		NeedOutputDrive: true,
	}}
	if got := m.nextStepAfter(stepStartup); got != stepOutputDrive {
		t.Errorf("only outputDrive: got %d, want stepOutputDrive(%d)", got, stepOutputDrive)
	}
}

// ── firstStep ────────────────────────────────────────────────────────────────

func TestFirstStepNeedFolder(t *testing.T) {
	m := setupModel{opts: SetupOptions{NeedFolder: true}}
	if got := m.firstStep(); got != stepFolder {
		t.Errorf("firstStep with NeedFolder: got %d, want stepFolder(%d)", got, stepFolder)
	}
}

func TestFirstStepNoFolder(t *testing.T) {
	m := setupModel{opts: SetupOptions{
		NeedFolder: false,
		NeedBypass: true,
	}}
	if got := m.firstStep(); got != stepBypass {
		t.Errorf("firstStep without folder: got %d, want stepBypass(%d)", got, stepBypass)
	}
}

func TestFirstStepNothingNeeded(t *testing.T) {
	m := setupModel{opts: SetupOptions{}}
	if got := m.firstStep(); got != stepDone {
		t.Errorf("firstStep with nothing needed: got %d, want stepDone(%d)", got, stepDone)
	}
}

func TestFirstStepOnlyParallelJobs(t *testing.T) {
	m := setupModel{opts: SetupOptions{NeedParallelJobs: true}}
	if got := m.firstStep(); got != stepParallelJobs {
		t.Errorf("firstStep only parallelJobs: got %d, want stepParallelJobs(%d)", got, stepParallelJobs)
	}
}

func TestFirstStepOnlyOutputDrive(t *testing.T) {
	m := setupModel{opts: SetupOptions{NeedOutputDrive: true}}
	if got := m.firstStep(); got != stepOutputDrive {
		t.Errorf("firstStep only outputDrive: got %d, want stepOutputDrive(%d)", got, stepOutputDrive)
	}
}

// ── scanPaths ────────────────────────────────────────────────────────────────

func TestScanPathsWithTempDir(t *testing.T) {
	root := t.TempDir()

	sub1 := filepath.Join(root, "sub1")
	sub2 := filepath.Join(root, "sub2")
	if err := os.MkdirAll(sub1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub2, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		filepath.Join(sub1, "video.mp4"),
		filepath.Join(sub1, "image.jpg"),
		filepath.Join(sub2, "clip.mkv"),
		filepath.Join(root, "doc.txt"),
	} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sc := scanPaths([]string{root}, []string{".mp4", ".mkv"})

	if sc.totalDirs != 3 {
		t.Errorf("totalDirs: got %d, want 3", sc.totalDirs)
	}
	if sc.totalFiles != 2 {
		t.Errorf("totalFiles: got %d, want 2", sc.totalFiles)
	}
	if len(sc.baseDirs) != 1 {
		t.Errorf("baseDirs: got %d entries, want 1", len(sc.baseDirs))
	}
}

func TestScanPathsNoExtensionFilter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc := scanPaths([]string{root}, nil)
	if sc.totalFiles != 2 {
		t.Errorf("totalFiles (no filter): got %d, want 2", sc.totalFiles)
	}
}

func TestScanPathsSingleFile(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "video.mp4")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc := scanPaths([]string{f}, []string{".mp4"})
	if sc.totalFiles != 1 {
		t.Errorf("single file totalFiles: got %d, want 1", sc.totalFiles)
	}
	if sc.totalDirs != 0 {
		t.Errorf("single file totalDirs: got %d, want 0", sc.totalDirs)
	}
}

func TestScanPathsDeduplicate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc := scanPaths([]string{root, root}, []string{".mp4"})
	if sc.totalFiles != 1 {
		t.Errorf("deduplicate totalFiles: got %d, want 1", sc.totalFiles)
	}
	if len(sc.baseDirs) != 1 {
		t.Errorf("deduplicate baseDirs: got %d, want 1", len(sc.baseDirs))
	}
}

// Choosing "different drive" before the async drive list has arrived must not
// silently fall back to the source drive on Enter.
func TestOutputDriveEnterIgnoredUntilDrivesLoaded(t *testing.T) {
	m := newSetupModel(SetupOptions{NeedOutputDrive: true})
	m.step = stepOutputDrive
	m.driveYesAsked = true
	m.driveWantsDiff = true

	m2, _ := m.updateStepOutputDrive(tea.KeyMsg{Type: tea.KeyEnter})
	if m2.step != stepOutputDrive {
		t.Fatalf("step = %v, want to stay on the drive list while it is empty", m2.step)
	}

	// Once drives arrive, Enter selects the highlighted one.
	m2.opts.AvailableDrives = []DriveInfo{{Root: `E:\`, Label: `E:\`}}
	m3, _ := m2.updateStepOutputDrive(tea.KeyMsg{Type: tea.KeyEnter})
	if m3.answers.OutputDrive != `E:\` {
		t.Errorf("OutputDrive = %q, want E:\\", m3.answers.OutputDrive)
	}
}
