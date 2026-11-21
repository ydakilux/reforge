package app

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ydakilux/reforge/internal/database"
)

func TestGroupFilesByFolder(t *testing.T) {
	sourceRoot := filepath.Join("C:", "Media", "Downloads")
	files := []database.RunFileRecord{
		{SourcePath: filepath.Join(sourceRoot, "FolderA", "file1.mp4"), OriginalSize: 100, ConvertedSize: 50},
		{SourcePath: filepath.Join(sourceRoot, "FolderA", "file2.mp4"), OriginalSize: 200, ConvertedSize: 80},
		{SourcePath: filepath.Join(sourceRoot, "FolderB", "file3.mp4"), OriginalSize: 300, ConvertedSize: 150},
		{SourcePath: filepath.Join(sourceRoot, "file_in_root.mp4"), OriginalSize: 400, ConvertedSize: 200},
	}

	groups := groupFilesByFolder(files, []string{sourceRoot})
	if len(groups) != 3 {
		t.Fatalf("expected 3 folder groups, got %d", len(groups))
	}

	// Group 0: root file (.)
	if groups[0].Name != "." {
		t.Errorf("expected group 0 name '.', got '%s'", groups[0].Name)
	}
	if groups[0].FileCount != 1 {
		t.Errorf("expected 1 file in root group, got %d", groups[0].FileCount)
	}

	// Group 1: FolderA
	if groups[1].Name != "FolderA" {
		t.Errorf("expected group 1 name 'FolderA', got '%s'", groups[1].Name)
	}
	if groups[1].FileCount != 2 {
		t.Errorf("expected 2 files in FolderA, got %d", groups[1].FileCount)
	}
	if groups[1].TotalOriginalBytes != 300 || groups[1].TotalConvertedBytes != 130 {
		t.Errorf("unexpected byte totals for FolderA: %d -> %d", groups[1].TotalOriginalBytes, groups[1].TotalConvertedBytes)
	}

	// Group 2: FolderB
	if groups[2].Name != "FolderB" {
		t.Errorf("expected group 2 name 'FolderB', got '%s'", groups[2].Name)
	}
	if groups[2].FileCount != 1 {
		t.Errorf("expected 1 file in FolderB, got %d", groups[2].FileCount)
	}
}

func TestRunsBrowserCollapseExpand(t *testing.T) {
	sourceRoot := filepath.Join("C:", "Media")
	files := []database.RunFileRecord{
		{SourcePath: filepath.Join(sourceRoot, "FolderA", "file1.mp4")},
		{SourcePath: filepath.Join(sourceRoot, "FolderA", "file2.mp4")},
		{SourcePath: filepath.Join(sourceRoot, "FolderB", "file3.mp4")},
	}

	m := newRunsBrowserModel(nil)
	m.pane = paneFiles
	m.files = files
	m.runs = []database.RunSummary{{ID: 1, SourcePaths: []string{sourceRoot}}}
	m.runCursor = 0
	m.loadedFor = 0
	m.width = 80
	m.height = 24

	// Initially all 2 folders are expanded -> total 2 folder headers + 3 files = 5 visible items
	_, items := m.getVisibleItems()
	if len(items) != 5 {
		t.Fatalf("expected 5 visible items initially, got %d", len(items))
	}

	// Press 'c' on first folder (FolderA) -> collapse FolderA
	m.fileCursor = 0
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(runsBrowserModel)

	if !m.collapsedFolders["FolderA"] {
		t.Errorf("expected FolderA to be collapsed")
	}

	_, items = m.getVisibleItems()
	// FolderA (1 header) + FolderB (1 header + 1 file) = 3 items
	if len(items) != 3 {
		t.Fatalf("expected 3 visible items after collapsing FolderA, got %d", len(items))
	}

	// Press 'C' (Shift+C) -> collapse all folders
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	m = updated.(runsBrowserModel)

	_, items = m.getVisibleItems()
	// 2 folder headers only
	if len(items) != 2 {
		t.Fatalf("expected 2 visible items after collapse all, got %d", len(items))
	}

	// Press 'E' (Shift+E) -> expand all folders
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	m = updated.(runsBrowserModel)

	_, items = m.getVisibleItems()
	if len(items) != 5 {
		t.Fatalf("expected 5 visible items after expand all, got %d", len(items))
	}
}

// A sibling folder that merely shares a name prefix with a source root must not
// be grouped as if it were inside that root.
func TestGroupFilesByFolder_PathBoundary(t *testing.T) {
	src := filepath.Join("C:", "Media", "Videos")
	files := []database.RunFileRecord{
		{SourcePath: filepath.Join(src, "Sub", "a.mp4")},
		{SourcePath: filepath.Join("C:", "Media", "Videos2", "x", "b.mp4")},
	}
	for _, g := range groupFilesByFolder(files, []string{src}) {
		if strings.HasPrefix(g.Name, "..") {
			t.Errorf("group %q escapes the source root (prefix match without path boundary)", g.Name)
		}
	}
}

func TestIsWithinDir(t *testing.T) {
	root := filepath.Join("C:", "Media", "Videos")
	cases := []struct {
		dir  string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "A", "B"), true},
		{strings.ToUpper(filepath.Join(root, "a")), true},
		{filepath.Join("C:", "Media", "Videos2"), false},
		{filepath.Join("C:", "Media"), false},
	}
	for _, c := range cases {
		if got := isWithinDir(c.dir, root); got != c.want {
			t.Errorf("isWithinDir(%q, %q) = %v, want %v", c.dir, root, got, c.want)
		}
	}
}

// Truncating a long folder line must keep the marker/prefix and the size info
// and never exceed the width (no left-chopped ANSI sequences).
func TestRenderFolderLineTruncationKeepsHead(t *testing.T) {
	src := filepath.Join("C:", "Media")
	long := strings.Repeat("VeryLongFolderName", 6)
	m := newRunsBrowserModel(nil)
	m.pane = paneFiles
	m.runs = []database.RunSummary{{ID: 1, SourcePaths: []string{src}}}
	m.files = []database.RunFileRecord{{SourcePath: filepath.Join(src, long, "a.mp4"), OriginalSize: 10, ConvertedSize: 5}}
	m.loadedFor = 0

	out, _ := m.renderDetailFilesFull(40)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "📁") {
			if lipgloss.Width(line) > 40 {
				t.Errorf("folder line wider than 40: %d", lipgloss.Width(line))
			}
			if !strings.Contains(line, "▼") {
				t.Errorf("folder marker lost by truncation: %q", line)
			}
			if !strings.Contains(line, "file(s)") {
				t.Errorf("folder info lost by truncation: %q", line)
			}
			return
		}
	}
	t.Fatal("no folder line rendered")
}

// Collapsing from a file row must leave the cursor on that folder's header, and
// "collapse all" must keep the cursor inside the (now shorter) list.
func TestCollapseKeepsCursorOnFolderHeader(t *testing.T) {
	src := filepath.Join("C:", "Media")
	m := newRunsBrowserModel(nil)
	m.pane = paneFiles
	m.files = []database.RunFileRecord{
		{SourcePath: filepath.Join(src, "A", "1.mp4")},
		{SourcePath: filepath.Join(src, "A", "2.mp4")},
		{SourcePath: filepath.Join(src, "B", "3.mp4")},
	}
	m.runs = []database.RunSummary{{ID: 1, SourcePaths: []string{src}}}
	m.loadedFor = 0
	m.width, m.height = 80, 24

	// items: [A][A1][A2][B][B3]; put the cursor on A2 and collapse.
	m.fileCursor = 2
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = u.(runsBrowserModel)
	_, items := m.getVisibleItems()
	if m.fileCursor < 0 || m.fileCursor >= len(items) || !items[m.fileCursor].isFolder || items[m.fileCursor].folderName != "A" {
		t.Fatalf("cursor %d not on folder A header; items=%+v", m.fileCursor, items)
	}

	// Cursor on a B file, collapse all: must stay in range, on B's header.
	m.collapsedFolders = map[string]bool{}
	m.fileCursor = 4
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	m = u.(runsBrowserModel)
	_, items = m.getVisibleItems()
	if m.fileCursor >= len(items) || items[m.fileCursor].folderName != "B" {
		t.Fatalf("after collapse-all cursor %d invalid; items=%+v", m.fileCursor, items)
	}
}
