package app

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
