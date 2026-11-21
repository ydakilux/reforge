package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ydakilux/reforge/internal/fileutil"
)

func (m setupModel) viewStepStartup() string {
	var b strings.Builder
	spinner := setupStyleSpinner.Render(spinnerFrames[m.startupTick%len(spinnerFrames)])
	b.WriteString(spinner + "  " + setupStyleLabel.Render("Initialising…") + "\n\n")
	for i, line := range m.startupLines {
		if i == len(m.startupLines)-1 {
			b.WriteString("  " + setupStyleStartupLine.Render(line) + "\n")
		} else {
			b.WriteString("  " + setupStyleStartupDim.Render(line) + "\n")
		}
	}
	b.WriteString("\n" + setupStyleHint.Render("  [Esc] cancel") + "\n")
	return b.String()
}

func (m setupModel) viewStepFolder(w int) string {
	inner := w - 4
	var b strings.Builder
	b.WriteString(setupStyleLabel.Render("Select input folder") + "\n")
	if m.fpGotoOverlay {
		hint := setupStyleHint.Render("[Enter] go   [Esc] cancel")
		b.WriteString(hint + "\n\n")
		b.WriteString(setupStyleHint.Render("  Path: ") + m.fpGotoInput.View() + "\n")
	} else if m.fpDriveOverlay {
		hint := setupStyleHint.Render("[↑/k][↓/j] navigate drives   [Enter] jump   [Tab/Esc] close")
		b.WriteString(hint + "\n")
		b.WriteString(setupStyleCurrent.Render("  "+m.fp.CurrentDirectory) + "\n\n")
		for i, d := range m.opts.AvailableDrives {
			cursor := "  "
			style := setupStyleDriveNormal
			if i == m.fpDriveCursor {
				cursor = setupStyleCursor.Render("❯ ")
				style = setupStyleDriveSelected
			}
			b.WriteString(style.Render(truncate(cursor+d.Label, inner)) + "\n")
		}
	} else {
		tabHint := ""
		if len(m.opts.AvailableDrives) > 0 {
			tabHint = "  [Tab] switch drive"
		}
		hint := setupStyleHint.Render("[Space] toggle highlighted  [←/h] up  [→/l/Enter] open  [d] remove last  [q] cancel" + tabHint + "  [Ctrl+G] go to path")
		b.WriteString(hint + "\n")
		b.WriteString(setupStyleCurrent.Render("  "+m.fp.CurrentDirectory) + "\n\n")
		b.WriteString(m.renderFpWithScrollbar(inner))
		if len(m.selectedPaths) > 0 {
			_, selH := m.splitHeights()
			itemRows := selH - 1
			if itemRows < 1 {
				itemRows = 1
			}
			paths := m.selectedPaths
			if len(paths) > itemRows {
				paths = paths[len(paths)-itemRows:]
			}
			b.WriteString("\n")
			label := fmt.Sprintf("  Selected (%d)", len(m.selectedPaths))
			if len(m.selectedPaths) > itemRows {
				label += fmt.Sprintf("  [showing last %d]", itemRows)
			}
			b.WriteString(setupStyleLabel.Render(label+"  [d] remove last  [c] Confirm") + "\n")
			for _, p := range paths {
				b.WriteString(setupStyleAnswer.Render("  ✓ "+truncate(p, inner-4)) + "\n")
			}
		}
	}
	return b.String()
}

func (m setupModel) viewStepConfirm(w int) string {
	inner := w - 4
	var b strings.Builder
	b.WriteString(setupStyleLabel.Render("Conversion overview") + "\n\n")
	b.WriteString(setupStyleHint.Render("  Directories : ") +
		setupStyleAnswer.Render(strconv.Itoa(m.scan.totalDirs)) + "\n")
	b.WriteString(setupStyleHint.Render("  Video files : ") +
		setupStyleAnswer.Render(strconv.Itoa(m.scan.totalFiles)) + "\n\n")
	b.WriteString(setupStyleLabel.Render("  Source paths:") + "\n")
	for _, d := range m.scan.baseDirs {
		b.WriteString(setupStyleAnswer.Render("    • "+truncate(d, inner-6)) + "\n")
	}
	b.WriteString("\n" + setupStyleHint.Render("  [Enter/Space] Start   [b/Backspace] Back   [q] Cancel") + "\n")
	return b.String()
}

func (m setupModel) viewStepYesNo() string {
	var b strings.Builder
	b.WriteString(m.renderAnsweredAbove())
	var question string
	if m.step == stepBypass {
		question = "Force re-conversion of already-processed files? (bypass DB check)"
	} else {
		question = "Re-compress files that are already HEVC/H.265?"
	}
	b.WriteString(setupStyleLabel.Render(question) + "\n")
	b.WriteString(setupStyleHint.Render("  [y] Yes   [n / Enter] No   [Esc] cancel") + "\n")
	return b.String()
}

func (m setupModel) viewStepParallelJobs() string {
	var b strings.Builder
	b.WriteString(m.renderAnsweredAbove())
	b.WriteString(setupStyleLabel.Render(
		fmt.Sprintf("Parallel conversion jobs  (recommended: %d)", m.opts.DefaultParallel),
	) + "\n")
	b.WriteString(setupStyleHint.Render("  Enter a number 1–8, or press Enter to accept the recommendation") + "\n  ")
	b.WriteString(m.ti.View() + "\n")
	return b.String()
}

func (m setupModel) viewStepOutputDrive(w int) string {
	inner := w - 4
	var b strings.Builder
	b.WriteString(m.renderAnsweredAbove())

	// Total file size for display — prefer scan result; fall back to pre-computed value.
	totalBytes := m.scan.totalSizeBytes
	if totalBytes == 0 {
		totalBytes = m.opts.TotalFileSizeBytes
	}

	if !m.driveYesAsked {
		b.WriteString(setupStyleLabel.Render("Write output to a different drive?") + "\n")
		if totalBytes > 0 {
			b.WriteString(setupStyleHint.Render(fmt.Sprintf("  Total video size to convert: %s", fileutil.FormatBytes(totalBytes))) + "\n")
		}
		b.WriteString(setupStyleHint.Render("  [y] Yes   [n / Enter] No — use source drive   [Esc] cancel") + "\n")
	} else {
		b.WriteString(setupStyleLabel.Render("Select output drive") + "\n")
		if totalBytes > 0 {
			b.WriteString(setupStyleHint.Render(fmt.Sprintf("  Space needed: %s", fileutil.FormatBytes(totalBytes))) + "\n")
		}
		b.WriteString(setupStyleHint.Render("  [↑/k] [↓/j] navigate   [Enter] confirm   [Esc] use source drive") + "\n\n")
		if len(m.opts.AvailableDrives) == 0 {
			b.WriteString(setupStyleHint.Render("  Detecting drives…  (press Esc to use the source drive)") + "\n")
		}
		for i, d := range m.opts.AvailableDrives {
			cursor := "  "
			var style lipgloss.Style
			if i == m.driveCursor {
				cursor = setupStyleCursor.Render("❯ ")
				style = setupStyleDriveSelected
			} else if totalBytes > 0 && d.FreeBytes > 0 {
				if d.FreeBytes >= totalBytes {
					style = setupStyleDriveEnough
				} else {
					style = setupStyleDriveInsufficient
				}
			} else {
				style = setupStyleDriveNormal
			}
			line := truncate(cursor+d.Label, inner)
			b.WriteString(style.Render(line) + "\n")
		}
	}
	return b.String()
}

// renderAnsweredAbove renders a compact summary of questions already answered.
func (m *setupModel) renderAnsweredAbove() string {
	var b strings.Builder
	if !m.opts.NeedFolder && len(m.answers.Paths) == 0 {
		// paths came from CLI — nothing to show
	} else if len(m.answers.Paths) > 0 {
		label := "Paths"
		if len(m.answers.Paths) == 1 {
			label = "Folder"
		}
		b.WriteString(setupStyleHint.Render("  "+label+"  ") +
			setupStyleAnswer.Render(strings.Join(m.answers.Paths, ", ")) + "\n")
	}
	if m.step > stepBypass && m.opts.NeedBypass {
		val := "No"
		if m.answers.Bypass {
			val = "Yes"
		}
		b.WriteString(setupStyleHint.Render("  Bypass DB check  ") +
			setupStyleAnswer.Render(val) + "\n")
	}
	if m.step > stepForceHevc && m.opts.NeedForceHevc {
		val := "No"
		if m.answers.ForceHevc {
			val = "Yes"
		}
		b.WriteString(setupStyleHint.Render("  Re-compress HEVC  ") +
			setupStyleAnswer.Render(val) + "\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

var (
	scrollThumbStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))
	scrollTrackStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#374151"))
)

func (m setupModel) renderFpWithScrollbar(maxWidth int) string {
	raw := m.fp.View()
	lines := strings.Split(raw, "\n")
	// The filepicker pads with trailing newlines up to Height; trim the final
	// empty element from the split but keep blank padding lines.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	total := len(m.fpFiles)
	visible := m.fp.Height
	needScroll := total > visible && visible > 0

	if !needScroll {
		return raw
	}

	scrollOffset := m.fpMin

	thumbSize := visible * visible / total
	if thumbSize < 1 {
		thumbSize = 1
	}

	maxOff := total - visible
	scrollRange := visible - thumbSize
	thumbStart := 0
	if maxOff > 0 {
		thumbStart = scrollOffset * scrollRange / maxOff
	}
	thumbEnd := thumbStart + thumbSize

	contentWidth := maxWidth - 2
	if contentWidth < 10 {
		contentWidth = 10
	}

	var b strings.Builder
	for i := 0; i < visible; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		visWidth := lipgloss.Width(line)
		if visWidth < contentWidth {
			line += strings.Repeat(" ", contentWidth-visWidth)
		}

		glyph := "░"
		style := scrollTrackStyle
		if i >= thumbStart && i < thumbEnd {
			glyph = "█"
			style = scrollThumbStyle
		}
		b.WriteString(line + style.Render(glyph) + "\n")
	}
	return b.String()
}
