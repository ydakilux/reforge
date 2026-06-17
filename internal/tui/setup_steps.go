package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ydakilux/reforge/internal/fileutil"
)

func (m setupModel) updateStepStartup(msg tea.KeyMsg) (setupModel, tea.Cmd) {
	if msg.String() == "ctrl+c" || msg.String() == "esc" {
		m.answers.Cancelled = true
		m.step = stepDone
		return m, nil
	}
	return m, nil
}

func (m setupModel) updateStepConfirm(msg tea.KeyMsg) (setupModel, tea.Cmd) {
	switch msg.String() {
	case "enter", " ":
		m.answers.Paths = make([]string, len(m.selectedPaths))
		copy(m.answers.Paths, m.selectedPaths)
		return m.advanceAndReturn()
	case "backspace", "b":
		m.step = stepFolder
		return m, nil
	case "ctrl+c", "esc", "q":
		m.answers.Cancelled = true
		m.step = stepDone
		return m, nil
	}
	return m, nil
}

func (m setupModel) updateStepYesNo(msg tea.KeyMsg) (setupModel, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		if m.step == stepBypass {
			m.answers.Bypass = true
		} else {
			m.answers.ForceHevc = true
		}
		return m.advanceAndReturn()
	case "n", "enter":
		return m.advanceAndReturn()
	case "ctrl+c", "esc":
		m.answers.Cancelled = true
		m.step = stepDone
		return m, nil
	}
	return m, nil
}

func (m setupModel) updateStepParallelJobs(msg tea.KeyMsg) (setupModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		n := m.opts.DefaultParallel
		if v, err := strconv.Atoi(strings.TrimSpace(m.ti.Value())); err == nil && v >= 1 {
			if v > 8 {
				v = 8
			}
			n = v
		}
		m.answers.ParallelJobs = n
		return m.advanceAndReturn()
	case "ctrl+c", "esc":
		m.answers.Cancelled = true
		m.step = stepDone
		return m, nil
	}
	// Fall through to text-input delegation.
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	return m, cmd
}

func (m setupModel) updateStepOutputDrive(msg tea.KeyMsg) (setupModel, tea.Cmd) {
	if !m.driveYesAsked {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.driveYesAsked = true
			m.driveWantsDiff = true
			return m, nil
		case "n", "enter":
			m.answers.OutputDrive = ""
			return m.advanceAndReturn()
		case "ctrl+c", "esc":
			m.answers.Cancelled = true
			m.step = stepDone
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		if m.driveCursor > 0 {
			m.driveCursor--
		}
	case "down", "j":
		if m.driveCursor < len(m.opts.AvailableDrives)-1 {
			m.driveCursor++
		}
	case "enter":
		if len(m.opts.AvailableDrives) > 0 {
			m.answers.OutputDrive = m.opts.AvailableDrives[m.driveCursor].Root
		}
		return m.advanceAndReturn()
	case "esc":
		m.answers.OutputDrive = ""
		return m.advanceAndReturn()
	case "ctrl+c":
		m.answers.Cancelled = true
		m.step = stepDone
		return m, nil
	}
	return m, nil
}

// updateStepDriveSpaceWarn handles the "destination may be full" confirmation.
// Y/Enter proceeds; N/Backspace returns to the drive selection step; Esc/Ctrl+C
// cancels setup.
func (m setupModel) updateStepDriveSpaceWarn(msg tea.KeyMsg) (setupModel, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y", "enter":
		// User accepted the risk — clear risks so nextStepAfter skips this step
		// from now on, and advance to stepDone.
		m.spaceRisks = nil
		m.step = stepDone
		return m, nil
	case "n", "backspace", "b":
		// Re-open drive selection so the user can pick another drive.
		if m.opts.NeedOutputDrive {
			m.spaceRisks = nil
			m.driveYesAsked = false
			m.driveWantsDiff = false
			m.answers.OutputDrive = ""
			m.step = stepOutputDrive
			return m, nil
		}
		// No drive selection available — treat as cancel.
		m.answers.Cancelled = true
		m.step = stepDone
		return m, nil
	case "ctrl+c", "esc":
		m.answers.Cancelled = true
		m.step = stepDone
		return m, nil
	}
	return m, nil
}

// computeSpaceRisks returns the list of destination drives whose free space is
// strictly less than the worst-case bytes that would be written to them.
//
// outputDrive == "" means "keep source drives" — risks are computed per source
// drive against its own files' total bytes.
// outputDrive != "" means everything is routed to that single drive — risk is
// computed against the sum of all source bytes.
//
// Worst-case assumption: converted output is the same size as the source. HEVC
// usually shrinks files, so the warning is conservative.
func (m *setupModel) computeSpaceRisks(outputDrive string) []driveSpaceRisk {
	bytesPerDrive := m.scan.bytesPerDrive
	if len(bytesPerDrive) == 0 {
		bytesPerDrive = m.opts.BytesPerDrive
	}

	// Build root → freeBytes map from AvailableDrives (case-insensitive on
	// Windows where the same root may differ in casing between sources).
	freeByRoot := make(map[string]int64, len(m.opts.AvailableDrives))
	for _, d := range m.opts.AvailableDrives {
		freeByRoot[strings.ToUpper(filepath.Clean(d.Root))] = d.FreeBytes
	}
	lookupFree := func(root string) (int64, bool) {
		v, ok := freeByRoot[strings.ToUpper(filepath.Clean(root))]
		return v, ok && v > 0
	}

	var risks []driveSpaceRisk

	if outputDrive == "" {
		// Per-drive check against bytes-on-that-drive.
		for root, need := range bytesPerDrive {
			if need <= 0 {
				continue
			}
			free, ok := lookupFree(root)
			if !ok {
				continue // unknown free space — don't warn
			}
			if free < need {
				risks = append(risks, driveSpaceRisk{root: root, freeBytes: free, needBytes: need})
			}
		}
		return risks
	}

	// Single destination drive — sum all source bytes.
	totalNeed := m.scan.totalSizeBytes
	if totalNeed == 0 {
		totalNeed = m.opts.TotalFileSizeBytes
	}
	if totalNeed <= 0 {
		return nil
	}
	free, ok := lookupFree(outputDrive)
	if !ok {
		return nil
	}
	if free < totalNeed {
		risks = append(risks, driveSpaceRisk{root: outputDrive, freeBytes: free, needBytes: totalNeed})
	}
	return risks
}

// viewStepDriveSpaceWarn renders the "destination may be full" confirmation.
func (m setupModel) viewStepDriveSpaceWarn(w int) string {
	var b strings.Builder
	b.WriteString(m.renderAnsweredAbove())
	b.WriteString(setupStyleWarn.Render("⚠  Destination may run out of space") + "\n\n")

	if m.answers.OutputDrive == "" {
		b.WriteString(setupStyleHint.Render("  You chose to keep the source drive(s) as output.") + "\n")
	} else {
		b.WriteString(setupStyleHint.Render(fmt.Sprintf("  Output drive: %s", m.answers.OutputDrive)) + "\n")
	}
	b.WriteString(setupStyleHint.Render("  Worst-case estimate assumes converted files are the same size as the source.") + "\n")
	b.WriteString(setupStyleHint.Render("  HEVC output is usually smaller, but this is not guaranteed.") + "\n\n")

	for _, r := range m.spaceRisks {
		short := fmt.Sprintf("  %s  free %s   need up to %s   shortfall %s",
			r.root,
			fileutil.FormatBytes(r.freeBytes),
			fileutil.FormatBytes(r.needBytes),
			fileutil.FormatBytes(r.needBytes-r.freeBytes),
		)
		b.WriteString(setupStyleDriveInsufficient.Render(short) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(setupStyleHint.Render("  Proceed anyway?") + "\n")
	b.WriteString(setupStyleHint.Render("  [y] Yes — proceed   [n / Backspace] Back to drive selection   [Esc] cancel") + "\n")
	return b.String()
}

func (m setupModel) updateStepFolder(msg tea.Msg) (setupModel, tea.Cmd) {
	if km, isKey := msg.(tea.KeyMsg); isKey {
		// Goto-directory overlay takes highest priority.
		if m.fpGotoOverlay {
			switch km.String() {
			case "enter":
				raw := strings.TrimSpace(m.fpGotoInput.Value())
				if raw == "" {
					m.fpGotoOverlay = false
					return m, nil
				}
				target := filepath.Clean(raw)
				info, err := os.Stat(target)
				if err != nil || !info.IsDir() {
					return m, nil
				}
				m.fpGotoOverlay = false
				m.fp.CurrentDirectory = target
				m.fp = resetFilepickerView(m.fp)
				m.fpLoadDir(target)
				return m, m.fp.Init()
			case "esc":
				m.fpGotoOverlay = false
				return m, nil
			case "ctrl+c":
				m.answers.Cancelled = true
				m.step = stepDone
				return m, nil
			}
			var cmd tea.Cmd
			m.fpGotoInput, cmd = m.fpGotoInput.Update(msg)
			return m, cmd
		}
		// Drive-switcher overlay takes priority.
		if m.fpDriveOverlay {
			switch km.String() {
			case "up", "k":
				if m.fpDriveCursor > 0 {
					m.fpDriveCursor--
				}
			case "down", "j":
				if m.fpDriveCursor < len(m.opts.AvailableDrives)-1 {
					m.fpDriveCursor++
				}
			case "enter":
				return m, m.jumpToDrive()
			case "tab", "esc":
				m.fpDriveOverlay = false
			case "ctrl+c":
				m.answers.Cancelled = true
				m.step = stepDone
				return m, nil
			}
			return m, nil // swallow all keys while overlay is open
		}
		// Normal folder-picker keys.
		switch km.String() {
		case "tab":
			if len(m.opts.AvailableDrives) > 0 {
				m.fpDriveOverlay = true
				m.fpDriveCursor = 0
				cur := strings.ToUpper(m.fp.CurrentDirectory)
				for i, d := range m.opts.AvailableDrives {
					if strings.HasPrefix(cur, strings.ToUpper(d.Root)) {
						m.fpDriveCursor = i
						break
					}
				}
			}
			return m, nil
		case "ctrl+g":
			m.fpGotoOverlay = true
			m.fpGotoInput.SetValue(m.fp.CurrentDirectory)
			m.fpGotoInput.CursorEnd()
			m.fpGotoInput.Focus()
			return m, nil
		case "ctrl+c":
			m.answers.Cancelled = true
			m.step = stepDone
			return m, nil
		case "q":
			m.answers.Cancelled = true
			m.step = stepDone
			return m, nil
		case " ":
			if m.fpCursor >= 0 && m.fpCursor < len(m.fpFiles) {
				entry := m.fpFiles[m.fpCursor]
				full := filepath.Join(m.fp.CurrentDirectory, entry.Name())
				m.selectedPaths = togglePath(m.selectedPaths, full)
				m.syncFpHeight()
			}
			return m, nil
		case "c":
			if len(m.selectedPaths) > 0 {
				m.scan = scanPaths(m.selectedPaths, m.opts.VideoExtensions)
				m.advance() // → stepConfirm
				return m, nil
			}
			return m, nil
		case "delete", "d":
			if len(m.selectedPaths) > 0 {
				m.selectedPaths = m.selectedPaths[:len(m.selectedPaths)-1]
				m.syncFpHeight()
			}
			return m, nil
		case "j", "down", "ctrl+n":
			if m.fpCursor < len(m.fpFiles)-1 {
				m.fpCursor++
				if m.fpCursor > m.fpMin+m.fp.Height-1 {
					m.fpMin++
				}
			}
		case "k", "up", "ctrl+p":
			if m.fpCursor > 0 {
				m.fpCursor--
				if m.fpCursor < m.fpMin {
					m.fpMin--
				}
			}
		case "g":
			m.fpCursor = 0
			m.fpMin = 0
		case "G":
			if len(m.fpFiles) > 0 {
				m.fpCursor = len(m.fpFiles) - 1
				m.fpMin = len(m.fpFiles) - m.fp.Height
				if m.fpMin < 0 {
					m.fpMin = 0
				}
			}
		case "J", "pgdown":
			m.fpCursor += m.fp.Height
			if m.fpCursor >= len(m.fpFiles) {
				m.fpCursor = len(m.fpFiles) - 1
			}
			m.fpMin += m.fp.Height
			if m.fpMin+m.fp.Height > len(m.fpFiles) {
				m.fpMin = len(m.fpFiles) - m.fp.Height
			}
			if m.fpMin < 0 {
				m.fpMin = 0
			}
		case "K", "pgup":
			m.fpCursor -= m.fp.Height
			if m.fpCursor < 0 {
				m.fpCursor = 0
			}
			m.fpMin -= m.fp.Height
			if m.fpMin < 0 {
				m.fpMin = 0
			}
		}
	}
	// Delegate to filepicker for navigation + directory loading.
	var cmd tea.Cmd
	prevDir := m.fp.CurrentDirectory
	m.fp, cmd = m.fp.Update(msg)
	if m.fp.CurrentDirectory != prevDir {
		m.fpLoadDir(m.fp.CurrentDirectory)
	}
	return m, cmd
}
