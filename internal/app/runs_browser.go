// Package app — runs_browser.go
//
// Two-pane interactive browser for conversion runs. Left pane lists runs
// (most recent first); right pane shows aggregate stats for the selected run
// plus the list of files converted in that run.
//
// Triggered from the launcher menu's "Recent runs" entry. Navigation:
//
//	↑/↓ or j/k   move selection in the active pane
//	Tab          toggle focus between run list and file list
//	Enter        on a run with no files loaded, force-load files
//	Esc/q        return to the launcher menu
//	Ctrl+C       quit reforge entirely
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ydakilux/reforge/internal/database"
)

// runsBrowserResult tells the caller whether the browser exited normally or
// the user pressed Ctrl+C to quit reforge.
type runsBrowserResult int

const (
	runsBrowserBack runsBrowserResult = iota
	runsBrowserQuit
)

// runRunsBrowser opens the run browser as a sub-program of the launcher.
// It returns runsBrowserQuit when the user pressed Ctrl+C, otherwise
// runsBrowserBack (Esc/q, or the program closing normally).
func runRunsBrowser(store database.Store) runsBrowserResult {
	m := newRunsBrowserModel(store)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Println("runs browser error:", err)
		return runsBrowserBack
	}
	if fm, ok := final.(runsBrowserModel); ok {
		return fm.result
	}
	return runsBrowserBack
}

// ── Model ────────────────────────────────────────────────────────────────────

type runsBrowserPane int

const (
	paneRuns runsBrowserPane = iota
	paneFiles
)

type runsBrowserModel struct {
	store database.Store

	runs       []database.RunSummary
	runsErr    error
	runCursor  int
	fileCursor int

	// files belong to the run at index loadedFor; when loadedFor != runCursor
	// the right-pane file list is stale and a reload is required.
	files     []database.RunFileRecord
	filesErr  error
	loadedFor int // -1 = nothing loaded

	// detailScroll is the first line of the right pane's rendered content
	// that should appear at the top of the visible pane. Updated by the
	// file-pane keys (j/k/↑/↓/PgUp/PgDn/Home/End) and reset whenever the
	// run selection changes.
	detailScroll int

	pane   runsBrowserPane
	width  int
	height int
	result runsBrowserResult
}

func newRunsBrowserModel(store database.Store) runsBrowserModel {
	return runsBrowserModel{
		store:     store,
		loadedFor: -1,
		result:    runsBrowserBack,
	}
}

// runsLoadedMsg / filesLoadedMsg are emitted by background commands so the
// UI does not block on database queries.
type runsLoadedMsg struct {
	runs []database.RunSummary
	err  error
}

type filesLoadedMsg struct {
	runID int64
	files []database.RunFileRecord
	err   error
}

func (m runsBrowserModel) Init() tea.Cmd {
	return m.loadRuns()
}

func (m runsBrowserModel) loadRuns() tea.Cmd {
	store := m.store
	return func() tea.Msg {
		if store == nil {
			return runsLoadedMsg{err: fmt.Errorf("database unavailable")}
		}
		runs, err := store.GetRuns(context.Background(), 0)
		return runsLoadedMsg{runs: runs, err: err}
	}
}

func (m runsBrowserModel) loadFilesFor(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.runs) {
		return nil
	}
	store := m.store
	runID := m.runs[idx].ID
	return func() tea.Msg {
		if store == nil {
			return filesLoadedMsg{runID: runID, err: fmt.Errorf("database unavailable")}
		}
		files, err := store.GetRunFiles(context.Background(), runID)
		return filesLoadedMsg{runID: runID, files: files, err: err}
	}
}

func (m runsBrowserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampDetailScroll()
		return m, nil

	case runsLoadedMsg:
		m.runs = msg.runs
		m.runsErr = msg.err
		if len(m.runs) > 0 {
			m.runCursor = 0
			m.fileCursor = 0
			m.detailScroll = 0
			return m, m.loadFilesFor(0)
		}
		return m, nil

	case filesLoadedMsg:
		// Ignore stale results for runs the user has since moved past.
		if m.runCursor < 0 || m.runCursor >= len(m.runs) {
			return m, nil
		}
		if m.runs[m.runCursor].ID != msg.runID {
			return m, nil
		}
		m.files = msg.files
		m.filesErr = msg.err
		m.loadedFor = m.runCursor
		m.fileCursor = 0
		m.detailScroll = 0
		m.clampDetailScroll()
		return m, nil

	case tea.KeyMsg:
		model, cmd := m.updateKey(msg)
		if rm, ok := model.(runsBrowserModel); ok {
			rm.clampDetailScroll()
			return rm, cmd
		}
		return model, cmd
	}
	return m, nil
}

func (m runsBrowserModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.result = runsBrowserQuit
		return m, tea.Quit
	case "esc", "q", "b", "backspace":
		m.result = runsBrowserBack
		return m, tea.Quit
	case "tab":
		if m.pane == paneRuns {
			m.pane = paneFiles
		} else {
			m.pane = paneRuns
		}
		return m, nil
	}

	switch m.pane {
	case paneRuns:
		return m.updateRunsPane(msg)
	case paneFiles:
		return m.updateFilesPane(msg)
	}
	return m, nil
}

func (m runsBrowserModel) updateRunsPane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.runs) == 0 {
		return m, nil
	}
	prev := m.runCursor
	switch msg.String() {
	case "up", "k":
		if m.runCursor > 0 {
			m.runCursor--
		}
	case "down", "j":
		if m.runCursor < len(m.runs)-1 {
			m.runCursor++
		}
	case "home", "g":
		m.runCursor = 0
	case "end", "G":
		m.runCursor = len(m.runs) - 1
	case "pgup":
		m.runCursor -= 10
		if m.runCursor < 0 {
			m.runCursor = 0
		}
	case "pgdown":
		m.runCursor += 10
		if m.runCursor >= len(m.runs) {
			m.runCursor = len(m.runs) - 1
		}
	case "enter":
		// Force reload even if already loaded.
		return m, m.loadFilesFor(m.runCursor)
	}
	if m.runCursor != prev {
		// Selection changed → load files for the new run and reset the
		// right-pane scroll position so the new run's meta is visible.
		m.files = nil
		m.filesErr = nil
		m.loadedFor = -1
		m.detailScroll = 0
		return m, m.loadFilesFor(m.runCursor)
	}
	return m, nil
}

func (m runsBrowserModel) updateFilesPane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The right pane is a single scrollable viewport over the combined
	// meta + file-list content. All movement keys scroll the viewport;
	// fileCursor tracks the highlighted file row (the first file visible
	// at the current scroll position) and is kept in sync below.
	// There is no separate auto-scroll-to-cursor logic, so PgUp/PgDn and
	// j/k/↑/↓ cannot fight each other.
	switch msg.String() {
	case "up", "k":
		if m.detailScroll > 0 {
			m.detailScroll--
		}
	case "down", "j":
		m.detailScroll++ // clamped in clampDetailScroll
	case "home", "g":
		m.detailScroll = 0
	case "end", "G":
		m.detailScroll = 1 << 30 // clamped in clampDetailScroll to last page
	case "pgup":
		m.detailScroll -= 10
		if m.detailScroll < 0 {
			m.detailScroll = 0
		}
	case "pgdown":
		m.detailScroll += 10 // clamped in clampDetailScroll
	}
	return m, nil
}

// ── View ─────────────────────────────────────────────────────────────────────

var (
	runsStyleTitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7C3AED")).
			Bold(true)
	runsStyleHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))
	runsStyleHeader = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A78BFA")).
			Bold(true)
	runsStylePaneFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#7C3AED")).
				Padding(0, 1)
	runsStylePaneBlur = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#374151")).
				Padding(0, 1)
	runsStyleSelected = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7C3AED")).
				Bold(true)
	runsStyleDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF"))
	runsStyleLegacy = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#92400E"))
	runsStyleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#DC2626"))
	runsStyleSaved = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981"))
	runsStyleSkipOnly = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#0EA5E9"))
)

func (m runsBrowserModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading…"
	}

	title := runsStyleTitle.Render("Reforge  —  Recent runs")
	hint := runsStyleHint.Render("[↑/↓] move   [PgUp/PgDn] scroll   [Tab] switch pane   [Enter] reload   [Esc/q] back   [Ctrl+C] quit")

	// Bubbletea's renderer expects the view to fit in (m.height - 1) rows so
	// the bottom row is left for the cursor and the alt-screen reset; writing
	// the full m.height rows can scroll the viewport and corrupt subsequent
	// frames. The vertical budget below targets m.height - 1 total rows:
	//   title         : 1
	//   hint          : 1
	//   blank line    : 1
	//   pane row      : innerH (lipgloss border adds 2 rows internally,
	//                   so the rendered pane occupies innerH + 2 rows)
	// → innerH = (m.height - 1) - 3 - 2 = m.height - 6
	frameH := m.height - 1
	if frameH < 6 {
		frameH = 6
	}
	innerH := frameH - 5
	if innerH < 4 {
		innerH = 4
	}
	leftW := m.width * 4 / 10
	if leftW < 32 {
		leftW = 32
	}
	rightW := m.width - leftW - 4 // 2 cols of border+padding per pane
	if rightW < 30 {
		rightW = 30
	}

	contentH := innerH
	left := m.renderRunsList(leftW-4, contentH)
	right := m.renderDetail(rightW-4, contentH)

	leftStyle := runsStylePaneBlur
	rightStyle := runsStylePaneBlur
	if m.pane == paneRuns {
		leftStyle = runsStylePaneFocused
	} else {
		rightStyle = runsStylePaneFocused
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top,
		leftStyle.Width(leftW).Height(innerH).Render(left),
		rightStyle.Width(rightW).Height(innerH).Render(right),
	)

	body := title + "\n" + hint + "\n\n" + row
	// Hard-clamp to frameH so the output never exceeds m.height-1 rows even
	// if a styled line wraps unexpectedly inside lipgloss. Place then pads
	// blank rows to keep the frame size constant across renders.
	body = clampLines(body, frameH)
	return lipgloss.Place(m.width, frameH, lipgloss.Left, lipgloss.Top, body)
}

// clampLines truncates s to at most n lines, used to cap list content
// before it is passed to a lipgloss border box.
func clampLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

func (m runsBrowserModel) renderRunsList(w, h int) string {
	if m.runsErr != nil {
		return runsStyleError.Render("Error loading runs: " + m.runsErr.Error())
	}
	if len(m.runs) == 0 {
		return runsStyleDim.Render("No runs recorded yet.\n\nStart a conversion and return here\nto browse it.")
	}

	var b strings.Builder
	b.WriteString(runsStyleHeader.Render(fmt.Sprintf("%d run(s)", len(m.runs))))
	b.WriteString("\n\n")

	// Header took 2 lines (label + blank). Each run renders as 2 lines
	// (date/size header + indented detail). Compute the sliding window so
	// the total output never exceeds h lines.
	avail := h - 2
	if avail < 2 {
		avail = 2
	}
	rows := avail / 2
	if rows < 2 {
		rows = 2
	}
	start := m.runCursor - rows/2
	if start < 0 {
		start = 0
	}
	end := start + rows
	if end > len(m.runs) {
		end = len(m.runs)
		start = end - rows
		if start < 0 {
			start = 0
		}
	}

	for i := start; i < end; i++ {
		r := m.runs[i]
		// Build the prefix inline (do NOT pre-render with style.Render here)
		// so the marker is not embedded as nested ANSI inside the outer
		// style.Render(head) call below. Nested ANSI causes lipgloss to
		// miscount the visual width and triggers spurious line wrapping.
		prefix := "  "
		style := runsStyleDim
		if i == m.runCursor {
			prefix = "❯ "
			style = runsStyleSelected
		}
		ts := shortTimestamp(r.StartedAt)
		head := fmt.Sprintf("%s%s  %d file(s)", prefix, ts, r.FileCount)
		b.WriteString(style.Render(head))
		if r.IsSkipOnly() {
			b.WriteString("  " + runsStyleSkipOnly.Render("[skip only]"))
		} else if r.Note == "legacy" {
			b.WriteString("  " + runsStyleLegacy.Render("[legacy]"))
		}
		b.WriteString("\n")

		var detail string
		switch {
		case r.IsSkipOnly():
			// Skip-only sessions did no real conversion work — show that
			// instead of the meaningless "0 B saved (0.0%)".
			detail = fmt.Sprintf("    %d skipped (already HEVC) — %s scanned",
				r.SkippedAlreadyHEVC, formatBytes(r.OriginalBytes))
		case r.OriginalBytes > 0:
			saved := r.OriginalBytes - r.ConvertedBytes
			pct := float64(saved) / float64(r.OriginalBytes) * 100
			detail = fmt.Sprintf("    %s → %s  (saved %s, %.1f%%)",
				formatBytes(r.OriginalBytes), formatBytes(r.ConvertedBytes),
				formatBytes(saved), pct)
		default:
			detail = "    (no size data)"
		}
		b.WriteString(runsStyleDim.Render(truncate(detail, w)))
		b.WriteString("\n")
	}
	return clampLines(b.String(), h)
}

// renderDetail builds the entire right-pane content for the selected run
// (meta block + Files section) as one big buffer, then slices it by the
// current scroll offset so the user can scroll through everything — sources,
// stats, and the full file list — with PgUp/PgDn while the right pane is
// focused. The actual scroll position (and auto-scroll-to-cursor logic) is
// computed in Update via clampDetailScroll so View is pure.
func (m runsBrowserModel) renderDetail(w, h int) string {
	if len(m.runs) == 0 {
		return runsStyleDim.Render("Select a run to see details.")
	}
	if m.runCursor < 0 || m.runCursor >= len(m.runs) {
		return ""
	}
	r := m.runs[m.runCursor]

	meta := m.renderDetailMeta(r, w)
	files, _ := m.renderDetailFilesFull(w)
	allLines := strings.Split(strings.TrimRight(meta+files, "\n"), "\n")
	total := len(allLines)

	viewH := h
	if viewH < 1 {
		viewH = 1
	}
	overflow := total > viewH
	if overflow {
		viewH-- // leave room for the indicator line
		if viewH < 1 {
			viewH = 1
		}
	}

	scroll := m.detailScroll
	maxScroll := total - viewH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	endIdx := scroll + viewH
	if endIdx > total {
		endIdx = total
	}
	out := strings.Join(allLines[scroll:endIdx], "\n")

	if overflow {
		top := scroll + 1
		bot := endIdx
		marker := "  "
		switch {
		case scroll > 0 && endIdx < total:
			marker = "↕ "
		case scroll > 0:
			marker = "↑ "
		case endIdx < total:
			marker = "↓ "
		}
		out += "\n" + runsStyleHint.Render(fmt.Sprintf("%s%d–%d of %d  ·  PgUp/PgDn to scroll", marker, top, bot, total))
	}

	// Hard-clamp to h lines so that any edge-case mismatch between
	// clampDetailScroll's total and this function's total never produces
	// more lines than the pane can hold.
	return clampLines(out, h)
}

// detailContentLines returns (totalLines, cursorLine) for the current run's
// right-pane content. cursorLine is -1 when no file row is highlighted. Used
// by Update to drive scroll clamping without rendering twice inside View.
func (m runsBrowserModel) detailContentLines(w int) (total int, cursorLine int) {
	if m.runCursor < 0 || m.runCursor >= len(m.runs) {
		return 0, -1
	}
	r := m.runs[m.runCursor]
	meta := m.renderDetailMeta(r, w)
	metaLines := countLines(meta)
	files, cl := m.renderDetailFilesFull(w)
	if cl >= 0 {
		cl += metaLines
	}
	total = countLines(meta + files)
	return total, cl
}

// detailViewport returns the width and height the right pane content has to
// fit in, matching the calculations View() uses. Kept here so Update can
// compute scroll bounds against the same numbers.
func (m runsBrowserModel) detailViewport() (w, h int) {
	frameH := m.height - 1
	if frameH < 6 {
		frameH = 6
	}
	innerH := frameH - 5
	if innerH < 4 {
		innerH = 4
	}
	leftW := m.width * 4 / 10
	if leftW < 32 {
		leftW = 32
	}
	rightW := m.width - leftW - 4
	if rightW < 30 {
		rightW = 30
	}
	return rightW - 4, innerH
}

// clampDetailScroll snaps m.detailScroll into [0, maxScroll] for the current
// pane size. Called at the end of every Update so View is a pure projection.
func (m *runsBrowserModel) clampDetailScroll() {
	if m.width == 0 || m.height == 0 {
		return
	}
	w, h := m.detailViewport()
	total, _ := m.detailContentLines(w)
	viewH := h
	if total > viewH {
		viewH-- // matches renderDetail: one line reserved for the indicator
		if viewH < 1 {
			viewH = 1
		}
	}
	maxScroll := total - viewH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.detailScroll > maxScroll {
		m.detailScroll = maxScroll
	}
	if m.detailScroll < 0 {
		m.detailScroll = 0
	}
}

// renderDetailMeta builds the top section of the right pane: identity,
// timestamps, encoder, output drive, source paths, and the stats block.
// This block must stay fully visible — it is rendered first and the file
// list is given only the remaining vertical budget.
func (m runsBrowserModel) renderDetailMeta(r database.RunSummary, w int) string {
	var b strings.Builder
	b.WriteString(runsStyleHeader.Render("Run #" + fmt.Sprintf("%d", r.ID)))
	if r.Note == "legacy" {
		b.WriteString("  " + runsStyleLegacy.Render("[legacy — synthesised from old records]"))
	}
	b.WriteString("\n")

	b.WriteString(runsStyleDim.Render("Started:  ") + shortTimestamp(r.StartedAt) + "\n")
	if r.EndedAt != "" {
		b.WriteString(runsStyleDim.Render("Ended:    ") + shortTimestamp(r.EndedAt) + "\n")
		if dur, ok := durationBetween(r.StartedAt, r.EndedAt); ok {
			b.WriteString(runsStyleDim.Render("Duration: ") + formatDuration(dur) + "\n")
		}
	}
	if r.Encoder != "" {
		b.WriteString(runsStyleDim.Render("Encoder:  ") + r.Encoder)
		if r.QualityPreset != "" {
			b.WriteString(runsStyleDim.Render("  preset ") + r.QualityPreset)
		}
		if r.ParallelJobs > 0 {
			b.WriteString(runsStyleDim.Render("  ×") + fmt.Sprintf("%d", r.ParallelJobs))
		}
		b.WriteString("\n")
	}

	// Destination first (short, single line) so it sits next to the other
	// metadata. Either the user-picked output drive, or the source drive
	// roots inferred from the run's converted files.
	dest := m.destinationLine(r)
	if dest != "" {
		b.WriteString(runsStyleDim.Render("Output:   ") + dest + "\n")
	}

	b.WriteString("\n")

	// Stats block — placed *above* Sources so the most useful summary is
	// visible without scrolling, even on runs with many source folders.
	// Skip-only sessions get a dedicated rendering so the user isn't left
	// wondering why "Saved" reads 0 B — the run literally did no conversion
	// work, it just scanned and skipped already-HEVC files.
	b.WriteString(runsStyleHeader.Render("Stats") + "\n")
	if r.IsSkipOnly() {
		b.WriteString("  " + runsStyleSkipOnly.Render("Skip-only session — nothing was converted") + "\n")
		b.WriteString(fmt.Sprintf("  Files scanned:  %d\n", r.FileCount))
		b.WriteString(fmt.Sprintf("  Already HEVC:   %d\n", r.SkippedAlreadyHEVC))
		b.WriteString(fmt.Sprintf("  Total size:     %s\n", formatBytes(r.OriginalBytes)))
	} else {
		b.WriteString(fmt.Sprintf("  Files:          %d\n", r.FileCount))
		if r.ConvertedKept > 0 {
			b.WriteString(fmt.Sprintf("  Converted:      %d\n", r.ConvertedKept))
		}
		if r.SkippedAlreadyHEVC > 0 {
			b.WriteString(fmt.Sprintf("  Already HEVC:   %d\n", r.SkippedAlreadyHEVC))
		}
		if r.NotBeneficialFiles > 0 {
			b.WriteString(fmt.Sprintf("  Not beneficial: %d\n", r.NotBeneficialFiles))
		} else if r.NotBeneficial > 0 {
			// Fall back to the value FinalizeRun stored on the run row
			// when no rows have note='not_beneficial' (e.g. legacy data).
			b.WriteString(fmt.Sprintf("  Not beneficial: %d\n", r.NotBeneficial))
		}
		if r.ErroredFiles > 0 {
			b.WriteString(fmt.Sprintf("  Errors:         %s\n", runsStyleError.Render(fmt.Sprintf("%d", r.ErroredFiles))))
		} else if r.ErrorCount > 0 {
			b.WriteString(fmt.Sprintf("  Errors:         %s\n", runsStyleError.Render(fmt.Sprintf("%d", r.ErrorCount))))
		}
		b.WriteString(fmt.Sprintf("  Original:       %s\n", formatBytes(r.OriginalBytes)))
		b.WriteString(fmt.Sprintf("  Converted:      %s\n", formatBytes(r.ConvertedBytes)))
		if r.OriginalBytes > 0 {
			saved := r.OriginalBytes - r.ConvertedBytes
			pct := float64(saved) / float64(r.OriginalBytes) * 100
			b.WriteString("  " + runsStyleSaved.Render(fmt.Sprintf("Saved:          %s  (%.1f%%)", formatBytes(saved), pct)) + "\n")
		}
	}
	b.WriteString("\n")

	// Source paths last — they can be long (legacy run 375 had 17 paths)
	// and live below Stats so the user can scroll into them without losing
	// the summary numbers above.
	if len(r.SourcePaths) > 0 {
		label := "Source:   "
		if len(r.SourcePaths) > 1 {
			label = fmt.Sprintf("Sources (%d):", len(r.SourcePaths))
			b.WriteString(runsStyleDim.Render(label) + "\n")
			for _, sp := range r.SourcePaths {
				b.WriteString("  " + truncate(sp, w-2) + "\n")
			}
		} else {
			b.WriteString(runsStyleDim.Render(label) + truncate(r.SourcePaths[0], w-10) + "\n")
		}
	} else if r.Note == "legacy" {
		b.WriteString(runsStyleDim.Render("Source:   ") + runsStyleDim.Render("(not recorded — legacy run)") + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// destinationLine returns a human-readable summary of where this run's output
// went. If the user picked an explicit output drive it returns that; otherwise
// it derives the destination drive roots from the loaded file list (when
// available) so the user can see "same drive as source" expanded into the
// actual drive letters.
func (m runsBrowserModel) destinationLine(r database.RunSummary) string {
	if r.OutputDrive != "" {
		return r.OutputDrive
	}
	if r.Note == "legacy" && len(m.files) == 0 {
		return "(not recorded — legacy run)"
	}
	// Derive from files when we have them loaded for this run.
	if m.loadedFor == m.runCursor && len(m.files) > 0 {
		roots := make(map[string]bool)
		for _, f := range m.files {
			if f.DriveRoot != "" {
				roots[f.DriveRoot] = true
			}
		}
		if len(roots) > 0 {
			list := make([]string, 0, len(roots))
			for k := range roots {
				list = append(list, k)
			}
			// Stable order.
			sortStrings(list)
			return strings.Join(list, ", ") + "  " + runsStyleDim.Render("(same drive as source)")
		}
	}
	return "(same drive as source)"
}

// renderDetailFilesFull renders the entire Files section (no sliding window)
// and returns the rendered string along with the relative line index of the
// highlighted row, or -1 when no row is highlighted (loading, error, empty,
// or files pane not focused). The right pane's scroll viewport slices this
// output to fit the available height.
func (m runsBrowserModel) renderDetailFilesFull(w int) (string, int) {
	var b strings.Builder
	b.WriteString(runsStyleHeader.Render("Files"))
	if m.pane == paneFiles {
		b.WriteString(runsStyleHint.Render("  ← focused"))
	} else {
		b.WriteString(runsStyleHint.Render("  (Tab to focus)"))
	}
	b.WriteString("\n")

	if m.filesErr != nil {
		b.WriteString(runsStyleError.Render("  Error: " + m.filesErr.Error()))
		return b.String(), -1
	}
	if m.loadedFor != m.runCursor {
		b.WriteString(runsStyleDim.Render("  loading…"))
		return b.String(), -1
	}
	if len(m.files) == 0 {
		b.WriteString(runsStyleDim.Render("  No files recorded for this run."))
		return b.String(), -1
	}

	// Header took 1 line. File rows start at line 1 (0-indexed within this
	// section). Track which absolute line the highlighted file lands on.
	cursorLine := -1
	for i, f := range m.files {
		isHighlighted := i == m.fileCursor && m.pane == paneFiles
		style := runsStyleDim
		if isHighlighted {
			style = runsStyleSelected
			cursorLine = 1 + i // +1 for the "Files" header line
		}
		name := f.SourcePath
		if name == "" {
			name = f.FileHash
		}
		status := "✓"
		if f.Error != "" {
			status = "✗"
			style = runsStyleError
		} else if f.Note == "not_beneficial" {
			status = "·"
		}
		// Build the prefix separately from the name so that the pre-rendered
		// ANSI marker is NOT embedded inside the outer style.Render() call.
		// Embedding pre-rendered ANSI inside another Render call causes
		// lipgloss to miscount the visual width and overflow the box.
		prefix := "  "
		if isHighlighted {
			prefix = "❯ "
		}
		size := fmt.Sprintf("%s → %s", formatBytes(f.OriginalSize), formatBytes(f.ConvertedSize))
		// Reserve enough columns for prefix(2) + status(1) + 2 separators(4) +
		// the size string. Truncate name to fill what's left, then truncate
		// the assembled line one more time as a hard cap so a long size
		// suffix can never push the visible width past w.
		fixed := lipgloss.Width(prefix) + lipgloss.Width(status) + 4 + lipgloss.Width(size)
		nameBudget := w - fixed
		if nameBudget < 4 {
			nameBudget = 4
		}
		line := fmt.Sprintf("%s%s  %s  %s", prefix, status, truncate(name, nameBudget), size)
		line = truncate(line, w)
		b.WriteString(style.Render(line) + "\n")
	}
	return b.String(), cursorLine
}

// countLines returns the number of newline-separated lines in s. A trailing
// newline counts as a terminator for the previous line, not a new empty line,
// matching how lipgloss renders content into a fixed-height box.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// sortStrings sorts in place. Tiny helper to avoid pulling in sort everywhere
// the runs browser needs deterministic ordering.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// ── small formatting helpers ─────────────────────────────────────────────────

// shortTimestamp parses an RFC3339-ish timestamp and renders it as "Jan 02
// 15:04". Returns the raw string on failure so the user still sees something.
func shortTimestamp(s string) string {
	if s == "" {
		return "—"
	}
	if t, ok := parseTimestamp(s); ok {
		return t.Local().Format("2006-01-02 15:04")
	}
	return s
}

func parseTimestamp(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func durationBetween(start, end string) (time.Duration, bool) {
	ts, ok1 := parseTimestamp(start)
	te, ok2 := parseTimestamp(end)
	if !ok1 || !ok2 {
		return 0, false
	}
	d := te.Sub(ts)
	if d < 0 {
		return 0, false
	}
	return d, true
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// truncate shortens s so its visible (terminal-cell) width does not exceed
// max columns. When truncation occurs, the LEFT side is dropped and a leading
// "…" is added so the most informative tail is preserved (filenames, sizes).
// Uses lipgloss.Width so multi-byte runes (→, …, etc.) and ANSI sequences
// are counted correctly.
func truncate(s string, max int) string {
	if max <= 1 {
		return s
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max <= 3 {
		// Not enough room for "…X"; fall back to byte slice.
		if len(s) > max {
			return s[:max]
		}
		return s
	}
	// Drop runes from the left until the remainder + "…" fits in max cells.
	runes := []rune(s)
	// Binary search for the largest tail that fits.
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi) / 2
		candidate := "…" + string(runes[mid:])
		if lipgloss.Width(candidate) <= max {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return "…" + string(runes[lo:])
}
