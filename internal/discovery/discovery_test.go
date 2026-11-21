package discovery_test

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/ydakilux/reforge/internal/discovery"
)

func newTestLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

func createFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create file %s: %v", path, err)
	}
}

func basenames(paths []string) []string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	sort.Strings(names)
	return names
}

func TestDiscoverFiles_BasicExtensionFilter(t *testing.T) {
	dir := t.TempDir()

	createFile(t, filepath.Join(dir, "video1.mp4"))
	createFile(t, filepath.Join(dir, "video2.avi"))
	createFile(t, filepath.Join(dir, "video3.mkv"))

	createFile(t, filepath.Join(dir, "readme.txt"))
	createFile(t, filepath.Join(dir, "photo.jpg"))
	createFile(t, filepath.Join(dir, "data.json"))

	extensions := []string{".MP4", ".AVI", ".MKV", ".MOV", ".WMV"}
	files, baseMap := discovery.DiscoverFiles([]string{dir}, extensions, newTestLogger())

	got := basenames(files)
	want := []string{"video1.mp4", "video2.avi", "video3.mkv"}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("expected %d files, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file[%d]: expected %s, got %s", i, want[i], got[i])
		}
	}

	for _, f := range files {
		if _, ok := baseMap[f]; !ok {
			t.Errorf("missing base-dir mapping for %s", f)
		}
	}
}

func TestDiscoverFiles_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()

	createFile(t, filepath.Join(dir, "lower.mp4"))
	createFile(t, filepath.Join(dir, "upper.MP4"))
	createFile(t, filepath.Join(dir, "mixed.Mp4"))

	extensions := []string{".MP4"}
	files, _ := discovery.DiscoverFiles([]string{dir}, extensions, newTestLogger())

	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(files), basenames(files))
	}
}

func TestDiscoverFiles_NestedDirectories(t *testing.T) {
	root := t.TempDir()

	createFile(t, filepath.Join(root, "top.mp4"))
	createFile(t, filepath.Join(root, "sub1", "deep1.avi"))
	createFile(t, filepath.Join(root, "sub1", "sub2", "deep2.mkv"))
	createFile(t, filepath.Join(root, "other", "deep3.mov"))

	createFile(t, filepath.Join(root, "sub1", "notes.txt"))

	extensions := []string{".MP4", ".AVI", ".MKV", ".MOV"}
	files, _ := discovery.DiscoverFiles([]string{root}, extensions, newTestLogger())

	got := basenames(files)
	want := []string{"deep1.avi", "deep2.mkv", "deep3.mov", "top.mp4"}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("expected %d files, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file[%d]: expected %s, got %s", i, want[i], got[i])
		}
	}
}

func TestDiscoverFiles_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	extensions := []string{".MP4", ".AVI", ".MKV"}
	files, baseMap := discovery.DiscoverFiles([]string{dir}, extensions, newTestLogger())

	if len(files) != 0 {
		t.Fatalf("expected 0 files for empty dir, got %d: %v", len(files), files)
	}
	if len(baseMap) != 0 {
		t.Fatalf("expected empty base-dir map, got %d entries", len(baseMap))
	}
}

func TestDiscoverFiles_MultiplePaths(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	createFile(t, filepath.Join(dir1, "a.mp4"))
	createFile(t, filepath.Join(dir1, "b.avi"))
	createFile(t, filepath.Join(dir2, "c.mkv"))
	createFile(t, filepath.Join(dir2, "d.wmv"))

	extensions := []string{".MP4", ".AVI", ".MKV", ".WMV"}
	files, baseMap := discovery.DiscoverFiles([]string{dir1, dir2}, extensions, newTestLogger())

	got := basenames(files)
	want := []string{"a.mp4", "b.avi", "c.mkv", "d.wmv"}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("expected %d files, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file[%d]: expected %s, got %s", i, want[i], got[i])
		}
	}

	absDir1, _ := filepath.Abs(dir1)
	absDir2, _ := filepath.Abs(dir2)
	for _, f := range files {
		base := baseMap[f]
		name := filepath.Base(f)
		switch name {
		case "a.mp4", "b.avi":
			if base != absDir1 {
				t.Errorf("%s: expected base dir %s, got %s", name, absDir1, base)
			}
		case "c.mkv", "d.wmv":
			if base != absDir2 {
				t.Errorf("%s: expected base dir %s, got %s", name, absDir2, base)
			}
		}
	}
}

func TestDiscoverFiles_NonexistentPath(t *testing.T) {
	fakePath := filepath.Join(t.TempDir(), "does_not_exist")

	extensions := []string{".MP4"}
	files, baseMap := discovery.DiscoverFiles([]string{fakePath}, extensions, newTestLogger())

	if len(files) != 0 {
		t.Fatalf("expected 0 files for nonexistent path, got %d: %v", len(files), files)
	}
	if len(baseMap) != 0 {
		t.Fatalf("expected empty base-dir map, got %d entries", len(baseMap))
	}
}

func TestDiscoverFiles_SkipsReforgedOutputDir(t *testing.T) {
	dir := t.TempDir()
	createFile(t, filepath.Join(dir, "a.mp4"))
	createFile(t, filepath.Join(dir, "REFORGED", "Videos", "a.mp4"))
	createFile(t, filepath.Join(dir, "REFORGED", "_TEMP", "x", "__tmp__abcd1234_1.mp4"))
	createFile(t, filepath.Join(dir, "Sub", "b.mp4"))

	files, _ := discovery.DiscoverFiles([]string{dir}, []string{".MP4"}, newTestLogger())
	if len(files) != 2 {
		t.Fatalf("got %d files %v, want 2 (REFORGED must be ignored)", len(files), files)
	}
	for _, f := range files {
		if filepath.Base(filepath.Dir(f)) == "Videos" || filepath.Base(f) == "__tmp__abcd1234_1.mp4" {
			t.Errorf("output artifact discovered: %s", f)
		}
	}
}

func TestDiscoverFiles_OverlappingPathsDeduplicated(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "Sub")
	createFile(t, filepath.Join(sub, "b.mp4"))
	createFile(t, filepath.Join(dir, "a.mp4"))

	files, baseMap := discovery.DiscoverFiles([]string{dir, sub}, []string{".MP4"}, newTestLogger())
	if len(files) != 2 {
		t.Fatalf("got %d files %v, want 2 (no duplicates)", len(files), files)
	}
	if got := baseMap[filepath.Join(sub, "b.mp4")]; got != dir {
		t.Errorf("base dir = %q, want first input path %q", got, dir)
	}
}
