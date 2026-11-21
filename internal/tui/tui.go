// Package tui provides a Bubble Tea–based terminal UI for Reforge.
//
// Layout (adapts to terminal width):
//
//	╭─ Reforge ─────────────────────────────────── ⏱ 00:02:34 ─╮
//	│                                                           │
//	│  [3/12] myvideo_long.mkv              1080p              │
//	│  ████████████████░░░░░░░░   64%  elapsed 0:45  ETA ~25s  │
//	│                                                           │
//	│  [4/12] another_film.mp4               720p              │
//	│  ██████░░░░░░░░░░░░░░░░░░   24%  elapsed 1:12  ETA ~3m   │
//	│                                                           │
//	├─ Log ─────────────────────────────────────────────────── ┤
//	│  ✓ KEPT    film_a.mkv   234.5 MB → 156.2 MB  (-33.3%)   │
//	│  ✗ DISCARD film_b.mp4   890.1 MB → 912.3 MB  (+2.5%)    │
//	╰─────────────────────────────────────────────────────────╯
//
// External goroutines (FFmpeg workers) drive the UI by calling methods on [UI].
// Those methods are thread-safe and send messages to the Bubble Tea program via
// [tea.Program.Send].
package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	colorAccent  = lipgloss.Color("#7C3AED") // purple
	colorOK      = lipgloss.Color("#22C55E") // green
	colorWarn    = lipgloss.Color("#F59E0B") // amber
	colorErr     = lipgloss.Color("#EF4444") // red
	colorMuted   = lipgloss.Color("#6B7280") // gray
	colorFill    = lipgloss.Color("#7C3AED")
	colorEmpty   = lipgloss.Color("#374151")
	colorBorder  = lipgloss.Color("#4B5563")
	colorTimer   = lipgloss.Color("#A78BFA")
	colorHeading = lipgloss.Color("#E5E7EB")

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	styleTitle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	styleTimer = lipgloss.NewStyle().
			Foreground(colorTimer).
			Bold(true)

	styleFileName = lipgloss.NewStyle().
			Foreground(colorHeading).
			Bold(true)

	styleMeta = lipgloss.NewStyle().
			Foreground(colorMuted)

	stylePct = lipgloss.NewStyle().
			Foreground(colorAccent).
			Width(5).
			Align(lipgloss.Right)

	styleElapsed = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleETA = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleLogOK = lipgloss.NewStyle().
			Foreground(colorOK)

	styleLogDiscard = lipgloss.NewStyle().
			Foreground(colorWarn)

	styleLogErr = lipgloss.NewStyle().
			Foreground(colorErr)

	styleLogInfo = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleSectionLabel = lipgloss.NewStyle().
				Foreground(colorMuted).
				Bold(false)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)
)

// ── Message types ─────────────────────────────────────────────────────────────

type msgJobStart struct {
	id           uint64
	label        string
	fileNum      int
	totalFiles   int
	heightP      int // video height in pixels (for resolution label)
	totalSeconds float64
}

type msgJobProgress struct {
	id  uint64
	pct int
}

type msgJobComplete struct {
	id      uint64
	summary string
	kind    completionKind
}

type msgLog struct{ text string }

// msgShowSummary is sent when all conversions are done.  The lines are appended
// to the log and the TUI enters "summary ready" mode where Enter/q quits.
type msgShowSummary struct{ lines []string }

type msgStartTimer struct{}

type msgTick time.Time

// msgSetConversionStats is sent once before the conversion loop starts to give
// the TUI enough context to compute a global ETA.
type msgSetConversionStats struct {
	totalFiles int
	totalBytes int64 // sum of sizes of all files that will be processed
}

// msgFileFinished is sent each time a file finishes (converted, skipped, or errored)
// so the global ETA can be updated from real throughput.
type msgFileFinished struct {
	sizeBytes int64 // original file size (0 if unknown)
}

// msgControlState updates the displayed control state (paused / stopping).
type msgControlState struct {
	paused   bool
	stopping StopKind
}

// StopKind distinguishes the three control states visible in the status bar.
type StopKind int

const (
	StopKindNone         StopKind = iota
	StopKindAfterCurrent          // "stopping after current…"
	StopKindNow                   // "cancelling…"
)

type completionKind int

const (
	kindKept completionKind = iota
	kindDiscard
	kindError
	kindSkipped
	kindSummary // used for the final stats block — rendered in bright heading colour
)

// tickCmd fires a repaint message every repaintInterval.
const repaintInterval = 250 * time.Millisecond

func tickCmd() tea.Cmd {
	return tea.Tick(repaintInterval, func(t time.Time) tea.Msg {
		return msgTick(t)
	})
}

// ── Job state ─────────────────────────────────────────────────────────────────

type jobState struct {
	id           uint64
	label        string
	fileNum      int
	totalFiles   int
	heightP      int
	pct          int
	startedAt    time.Time
	totalSeconds float64
}

// pauseSpan is one pause interval; to is zero while the pause is ongoing.
type pauseSpan struct{ from, to time.Time }

// activeDuration returns the time between from and to excluding every pause,
// so timers and ETAs do not keep counting while processing is suspended.
func (m model) activeDuration(from, to time.Time) time.Duration {
	if from.IsZero() || !to.After(from) {
		return 0
	}
	total := to.Sub(from)
	for _, p := range m.pauses {
		s, e := p.from, p.to
		if e.IsZero() || e.After(to) {
			e = to
		}
		if s.Before(from) {
			s = from
		}
		if e.After(s) {
			total -= e.Sub(s)
		}
	}
	return total
}

func (m model) jobElapsed(j *jobState) time.Duration {
	return m.activeDuration(j.startedAt, time.Now()).Round(time.Second)
}

func (m model) jobETA(j *jobState) string {
	if j.pct <= 0 {
		return "–"
	}
	elapsed := m.activeDuration(j.startedAt, time.Now())
	remaining := time.Duration(float64(elapsed) * float64(100-j.pct) / float64(j.pct))
	return "~" + fmtDuration(remaining.Round(time.Second))
}

// ── Phase enum ────────────────────────────────────────────────────────────────

type tuiPhase int

const (
	phaseSetup      tuiPhase = iota // setup questions (folder picker + yes/no + numeric)
	phaseConverting                 // main conversion display
)

// ── Model ─────────────────────────────────────────────────────────────────────

const maxLogLines = 200

type model struct {
	phase tuiPhase
	setup setupModel
	// answersCh receives the SetupAnswers once setup completes and is then closed.
	answersCh chan<- SetupAnswers

	width       int
	height      int
	jobs        []*jobState          // active jobs, in insertion order
	jobIndex    map[uint64]*jobState // fast lookup
	logLines    []logLine
	vp          viewport.Model
	vpReady     bool
	timerStart  time.Time
	timerActive bool
	// scroll state: true when the user has manually scrolled up
	userScrolled bool
	// summaryReady is true after ShowSummary has delivered its lines; in this
	// state Enter/q quits the TUI instead of triggering control actions.
	summaryReady bool
	// control state
	paused    bool
	stopKind  StopKind
	onControl func(action controlAction) // called on key press
	// confirmQuit is true while waiting for the user to confirm an immediate
	// cancel (q / Ctrl+C), so a stray keypress cannot kill running encodes.
	confirmQuit bool
	// pauses records every pause interval so elapsed/ETA exclude paused time.
	pauses []pauseSpan
	// onScreenReady is called once on the first WindowSizeMsg (either phase).
	onScreenReady func()
	// onReady is called once after the converting phase viewport is initialised.
	onReady func()

	// global ETA tracking
	etaTotalFiles    int       // total files to process (0 = unknown)
	etaTotalBytes    int64     // total bytes across all files (0 = unknown)
	etaDoneFiles     int       // files completed (converted + skipped + errored)
	etaDoneBytes     int64     // bytes of completed files
	etaFirstDoneTime time.Time // wall time of first file completion
}

type controlAction int

const (
	actionPause controlAction = iota // toggle pause/resume
	actionStopAfterCurrent
	actionStopNow
)

type logLine struct {
	text string
	kind completionKind
}

func initialModel(setupOpts SetupOptions, answersCh chan<- SetupAnswers) model {
	sm := newSetupModel(setupOpts)
	phase := phaseSetup
	// If setup has nothing to ask, skip straight to converting and deliver
	// zero-value answers immediately.
	if sm.done() {
		close(answersCh)
		phase = phaseConverting
	}
	return model{
		phase:     phase,
		setup:     sm,
		answersCh: answersCh,
		width:     80,
		height:    24,
		jobIndex:  make(map[uint64]*jobState),
	}
}

func (m model) Init() tea.Cmd {
	if m.phase == phaseSetup {
		return tea.Batch(m.setup.init(), tickCmd(), tea.EnableMouseCellMotion)
	}
	return tea.Batch(tickCmd(), tea.EnableMouseCellMotion)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// ── Setup phase ───────────────────────────────────────────────────────────
	if m.phase == phaseSetup {
		// Keep the repaint loop alive: if it dies here nothing restarts it, and
		// the converting phase would only repaint when some other message
		// arrives (timers/ETA frozen, nothing at all while paused).
		if _, ok := msg.(msgTick); ok {
			return m, tickCmd()
		}
		// Always propagate window size to setup sub-model.
		if ws, ok := msg.(tea.WindowSizeMsg); ok {
			m.width = ws.Width
			m.height = ws.Height
			// Signal alt-screen is ready.
			if m.onScreenReady != nil {
				fn := m.onScreenReady
				m.onScreenReady = nil
				go fn()
			}
			var cmd tea.Cmd
			m.setup, cmd = m.setup.update(msg)
			return m, cmd
		}

		var cmd tea.Cmd
		m.setup, cmd = m.setup.update(msg)

		if m.setup.done() {
			// Deliver answers and transition phase.
			if m.answersCh != nil {
				m.answersCh <- m.setup.answers
				close(m.answersCh)
				m.answersCh = nil
			}
			m.phase = phaseConverting
			// Fire onReady immediately if we already have a size; otherwise
			// wait for the first WindowSizeMsg in converting phase.
			if m.width > 0 && m.height > 0 {
				m.vp = viewport.New(m.logViewportWidth(), m.logViewportHeight())
				m.vp.SetContent(m.renderLogContent())
				m.vp.GotoBottom()
				m.vpReady = true
				if m.onReady != nil {
					fn := m.onReady
					m.onReady = nil
					return m, tea.Batch(cmd, func() tea.Msg { fn(); return nil })
				}
			}
		}
		return m, cmd
	}

	// ── Converting phase ──────────────────────────────────────────────────────
	switch msg := msg.(type) {

	case tea.KeyMsg:
		// In summary-ready mode the only action is to quit.
		if m.summaryReady {
			switch msg.String() {
			case "enter", "q", "Q", "ctrl+c", " ":
				return m, tea.Quit
			}
			// still allow scroll keys
		}
		// Waiting for confirmation of an immediate cancel: only y / Ctrl+C
		// confirm; any other key dismisses the prompt.
		if m.confirmQuit {
			m.confirmQuit = false
			switch msg.String() {
			case "y", "Y", "ctrl+c":
				if m.onControl != nil && m.stopKind != StopKindNow {
					fn := m.onControl
					return m, func() tea.Msg { fn(actionStopNow); return nil }
				}
			}
			return m, nil
		}
		// Scroll keys go to the viewport; all others go to control handler.
		switch msg.String() {
		case "up", "k":
			if m.vpReady {
				m.vp.LineUp(1)
				m.userScrolled = !m.vp.AtBottom()
			}
			return m, nil
		case "down", "j":
			if m.vpReady {
				m.vp.LineDown(1)
				m.userScrolled = !m.vp.AtBottom()
			}
			return m, nil
		case "pgup", "b":
			if m.vpReady {
				m.vp.HalfViewUp()
				m.userScrolled = !m.vp.AtBottom()
			}
			return m, nil
		case "pgdown", "f", " ":
			if m.vpReady {
				m.vp.HalfViewDown()
				m.userScrolled = !m.vp.AtBottom()
			}
			return m, nil
		case "G", "end":
			if m.vpReady {
				m.vp.GotoBottom()
				m.userScrolled = false
			}
			return m, nil
		}
		if m.onControl != nil {
			switch msg.String() {
			case "p", "P":
				fn := m.onControl
				return m, func() tea.Msg { fn(actionPause); return nil }
			case "s", "S":
				if m.stopKind == StopKindNone {
					fn := m.onControl
					return m, func() tea.Msg { fn(actionStopAfterCurrent); return nil }
				}
			case "q", "Q", "ctrl+c":
				if m.stopKind != StopKindNow {
					m.confirmQuit = true
				}
				return m, nil
			}
		}
		return m, nil

	case msgControlState:
		now := time.Now()
		if msg.paused && !m.paused {
			m.pauses = append(m.pauses, pauseSpan{from: now})
		} else if !msg.paused && m.paused && len(m.pauses) > 0 {
			m.pauses[len(m.pauses)-1].to = now
		}
		m.paused = msg.paused
		m.stopKind = msg.stopping
		if m.stopKind == StopKindNow {
			m.confirmQuit = false
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Signal alt-screen is ready (first time only).
		if m.onScreenReady != nil {
			fn := m.onScreenReady
			m.onScreenReady = nil
			go fn()
		}
		if !m.vpReady {
			m.vp = viewport.New(m.logViewportWidth(), m.logViewportHeight())
			m.vp.SetContent(m.renderLogContent())
			m.vp.GotoBottom()
			m.vpReady = true
			// Signal that the TUI is ready to receive messages.
			if m.onReady != nil {
				fn := m.onReady
				m.onReady = nil // fire only once
				return m, func() tea.Msg { fn(); return nil }
			}
		} else {
			m.vp.Width = m.logViewportWidth()
			m.vp.Height = m.logViewportHeight()
			m.vp.SetContent(m.renderLogContent())
		}
		return m, nil

	case msgTick:
		// Rerender on each tick so elapsed/ETA update live.
		// Also keep viewport content fresh.
		if m.vpReady {
			m.vp.SetContent(m.renderLogContent())
			if !m.userScrolled {
				m.vp.GotoBottom()
			}
		}
		return m, tickCmd()

	case msgStartTimer:
		m.timerStart = time.Now()
		m.timerActive = true
		return m, nil

	case msgJobStart:
		js := &jobState{
			id:           msg.id,
			label:        msg.label,
			fileNum:      msg.fileNum,
			totalFiles:   msg.totalFiles,
			heightP:      msg.heightP,
			startedAt:    time.Now(),
			totalSeconds: msg.totalSeconds,
		}
		m.jobs = append(m.jobs, js)
		m.jobIndex[msg.id] = js
		if m.vpReady {
			m.vp.Height = m.logViewportHeight()
		}
		return m, nil

	case msgJobProgress:
		if js, ok := m.jobIndex[msg.id]; ok {
			js.pct = clampPct(msg.pct)
		}
		return m, nil

	case msgJobComplete:
		// Remove from active jobs
		if js, ok := m.jobIndex[msg.id]; ok {
			delete(m.jobIndex, msg.id)
			for i, j := range m.jobs {
				if j == js {
					m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
					break
				}
			}
		}
		// Append to log
		m.logLines = append(m.logLines, logLine{text: msg.summary, kind: msg.kind})
		if len(m.logLines) > maxLogLines {
			m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
		}
		if m.vpReady {
			m.vp.Height = m.logViewportHeight()
			m.vp.SetContent(m.renderLogContent())
			if !m.userScrolled {
				m.vp.GotoBottom()
			}
		}
		return m, nil

	case msgLog:
		m.logLines = append(m.logLines, logLine{text: msg.text, kind: kindSkipped})
		if len(m.logLines) > maxLogLines {
			m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
		}
		if m.vpReady {
			m.vp.SetContent(m.renderLogContent())
			if !m.userScrolled {
				m.vp.GotoBottom()
			}
		}
		return m, nil

	case msgShowSummary:
		// Append a blank separator then all summary lines.
		m.logLines = append(m.logLines, logLine{text: "", kind: kindSkipped})
		for _, l := range msg.lines {
			m.logLines = append(m.logLines, logLine{text: l, kind: kindSummary})
		}
		if len(m.logLines) > maxLogLines {
			m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
		}
		m.summaryReady = true
		if m.vpReady {
			m.vp.Height = m.logViewportHeight()
			m.vp.SetContent(m.renderLogContent())
			m.vp.GotoBottom()
			m.userScrolled = false
		}
		return m, nil

	case msgSetConversionStats:
		m.etaTotalFiles = msg.totalFiles
		m.etaTotalBytes = msg.totalBytes
		return m, nil

	case msgFileFinished:
		m.etaDoneFiles++
		m.etaDoneBytes += msg.sizeBytes
		if m.etaFirstDoneTime.IsZero() {
			m.etaFirstDoneTime = time.Now()
		}
		return m, nil
	}

	// Forward mouse and other events to viewport
	var cmd tea.Cmd
	prevOffset := m.vp.YOffset
	m.vp, cmd = m.vp.Update(msg)
	if m.vp.YOffset != prevOffset {
		m.userScrolled = !m.vp.AtBottom()
	}
	return m, cmd
}

// globalETA returns the estimated total duration for all files ("~X:XX") or
// "" when there is not enough data yet.
//
// Strategy:
//  1. If active jobs have meaningful progress (pct > 0), estimate each job's
//     remaining seconds from its own elapsed/pct ratio, then project queued
//     files using the average per-job speed and current parallelism.
//  2. Once files start completing, blend in throughput from completed files
//     (bytes or count based) for the queued portion — this becomes the dominant
//     signal as more files finish.
func (m model) globalETA() string {
	if !m.timerActive {
		return ""
	}

	now := time.Now()
	totalElapsed := m.activeDuration(m.timerStart, now)

	// ── gather per-job remaining seconds from active jobs ──────────────────
	// Only use jobs that have made measurable progress.
	var totalJobRemainingSeconds float64
	var validJobs int
	var avgSecsPerPct float64 // averaged over all active valid jobs

	for _, js := range m.jobs {
		if js.pct <= 0 {
			continue
		}
		jobElapsed := m.activeDuration(js.startedAt, now).Seconds()
		if jobElapsed <= 0 {
			continue
		}
		secsPerPct := jobElapsed / float64(js.pct)
		remaining := secsPerPct * float64(100-js.pct)
		totalJobRemainingSeconds += remaining
		avgSecsPerPct += secsPerPct
		validJobs++
	}

	// Not enough data at all.
	if validJobs == 0 && m.etaDoneFiles == 0 {
		return ""
	}

	parallelism := len(m.jobs)
	if parallelism < 1 {
		parallelism = 1
	}

	if validJobs > 0 {
		avgSecsPerPct /= float64(validJobs)
	}

	// ── compute queued files count ─────────────────────────────────────────
	// etaTotalFiles comes from SetConversionStats. Fallback: use totalFiles
	// field on any active job (it's the grand total, not per-folder).
	totalFiles := m.etaTotalFiles
	if totalFiles == 0 {
		for _, js := range m.jobs {
			if js.totalFiles > 0 {
				totalFiles = js.totalFiles
				break
			}
		}
	}

	doneFiles := m.etaDoneFiles // fully completed
	activeFiles := len(m.jobs)  // currently converting
	queuedFiles := 0
	if totalFiles > 0 {
		queuedFiles = totalFiles - doneFiles - activeFiles
		if queuedFiles < 0 {
			queuedFiles = 0
		}
	}

	// ── remaining time estimate ────────────────────────────────────────────
	var remainingSecs float64

	if validJobs > 0 {
		// Active jobs: use their individual ETA (max of active jobs, since they
		// run in parallel — the slowest one dictates when the batch finishes).
		maxJobRemaining := 0.0
		for _, js := range m.jobs {
			if js.pct <= 0 {
				continue
			}
			jobElapsed := m.activeDuration(js.startedAt, now).Seconds()
			if jobElapsed <= 0 {
				continue
			}
			r := (jobElapsed / float64(js.pct)) * float64(100-js.pct)
			if r > maxJobRemaining {
				maxJobRemaining = r
			}
		}
		remainingSecs = maxJobRemaining

		// Queued files: use avg speed from active jobs.
		if queuedFiles > 0 && avgSecsPerPct > 0 {
			secsPerFile := avgSecsPerPct * 100.0
			queuedSecs := secsPerFile * float64(queuedFiles) / float64(parallelism)
			remainingSecs += queuedSecs
		}
	}

	// ── blend with completion-based throughput once files start finishing ──
	if m.etaDoneFiles > 0 && !m.etaFirstDoneTime.IsZero() {
		sinceFirstDone := m.activeDuration(m.etaFirstDoneTime, now).Seconds()
		if sinceFirstDone > 0 {
			var completionBasedRemaining float64
			if m.etaTotalBytes > 0 && m.etaDoneBytes > 0 {
				bytesPerSec := float64(m.etaDoneBytes) / sinceFirstDone
				remainingBytes := float64(m.etaTotalBytes - m.etaDoneBytes)
				// Subtract bytes currently being processed (rough approximation).
				for _, js := range m.jobs {
					if js.pct > 0 && m.etaTotalBytes > 0 && totalFiles > 0 {
						approxJobBytes := float64(m.etaTotalBytes) / float64(totalFiles)
						remainingBytes -= approxJobBytes * float64(js.pct) / 100.0
					}
				}
				if remainingBytes < 0 {
					remainingBytes = 0
				}
				if bytesPerSec > 0 {
					completionBasedRemaining = remainingBytes / bytesPerSec
				}
			} else if totalFiles > 0 {
				secPerFile := sinceFirstDone / float64(m.etaDoneFiles)
				completionBasedRemaining = secPerFile * float64(queuedFiles+activeFiles) / float64(parallelism)
			}

			// Weight: completion-based grows in influence as more files finish.
			weight := float64(m.etaDoneFiles) / float64(m.etaDoneFiles+activeFiles+1)
			remainingSecs = remainingSecs*(1-weight) + completionBasedRemaining*weight
		}
	}

	if remainingSecs < 0 {
		remainingSecs = 0
	}

	totalEstimate := totalElapsed + time.Duration(remainingSecs*float64(time.Second))
	if totalEstimate <= 0 {
		return ""
	}

	return "~" + fmtDuration(totalEstimate.Round(time.Second))
}

func (m model) View() string {
	if m.phase == phaseSetup {
		return m.setup.view()
	}
	inner := m.renderInner()
	// Wrap in rounded border, respecting terminal width
	available := m.width - 2 // 2 for border
	if available < 20 {
		available = 20
	}
	return styleBorder.Width(available).Render(inner) + "\n"
}

func (m model) renderInner() string {
	w := m.innerWidth()
	var b strings.Builder

	// ── Title bar ──
	title := styleTitle.Render("Reforge")
	var timerStr string
	if m.timerActive {
		elapsed := m.activeDuration(m.timerStart, time.Now()).Round(time.Second)
		timerPart := styleTimer.Render("⏱ " + fmtDuration(elapsed))
		if eta := m.globalETA(); eta != "" {
			timerStr = timerPart + styleTimer.Render(" / "+eta)
		} else {
			timerStr = timerPart
		}
	}
	titleLine := padBetween(title, timerStr, w)
	b.WriteString(titleLine + "\n")

	// ── Control status bar ──
	b.WriteString(m.renderControlBar(w) + "\n")

	// ── Active jobs ──
	if len(m.jobs) > 0 {
		b.WriteString("\n")
		for _, js := range m.jobs {
			b.WriteString(m.renderJob(js, w))
		}
	}

	// ── Log section ──
	var logLabel string
	if m.userScrolled && m.vpReady {
		pct := m.vp.ScrollPercent()
		logLabel = fmt.Sprintf("Log  %d%%", int(pct*100))
	} else {
		logLabel = "Log"
	}
	sep := m.renderSectionSep(logLabel, w)
	b.WriteString(sep + "\n")
	// Viewport for scrolling log
	if !m.vpReady {
		// First time: initialize viewport
		// (can't mutate m here; do it next tick after size is known)
		// For now render log lines directly
		for _, ll := range m.lastLogLines(m.logHeight()) {
			b.WriteString(m.renderLogLine(ll, w) + "\n")
		}
	} else {
		b.WriteString(m.vp.View() + "\n")
	}

	return b.String()
}

// renderControlBar renders the one-line hint/status row just below the title.
func (m model) renderControlBar(w int) string {
	var status string
	switch {
	case m.summaryReady:
		status = styleLogOK.Render("✓ Conversion complete — [Enter/q] Exit  ↑↓/PgUp/PgDn scroll")
	case m.confirmQuit:
		status = styleLogErr.Render("⚠  Cancel all conversions now?  [y] yes, cancel   [any other key] keep going")
	case m.stopKind == StopKindNow:
		status = styleLogErr.Render("⛔ Cancelling — waiting for FFmpeg to stop…")
	case m.stopKind == StopKindAfterCurrent:
		status = styleLogDiscard.Render("⏹  Finishing current files, then stopping…  [q] cancel now")
	case m.paused:
		status = styleTimer.Render("⏸  Paused — [p] resume  [s] finish current  [q] cancel now")
	case m.userScrolled:
		status = styleMuted.Render("[p] pause  [s] finish current  [q] cancel now  ↑↓/PgUp/PgDn scroll  [G] follow")
	default:
		status = styleMuted.Render("[p] pause  [s] finish current  [q] cancel now  ↑↓/PgUp/PgDn scroll")
	}
	// Pad/truncate to width
	sw := lipgloss.Width(status)
	if sw < w {
		status += strings.Repeat(" ", w-sw)
	}
	return truncateANSI(status, w)
}

func (m model) renderJob(js *jobState, w int) string {
	var b strings.Builder

	// Row 1: filename + resolution
	label := truncate(js.label, w-12)
	res := ""
	if js.heightP > 0 {
		res = styleMeta.Render(fmt.Sprintf("%dp", js.heightP))
	}
	counter := styleMeta.Render(fmt.Sprintf("[%d/%d]", js.fileNum, js.totalFiles))
	nameLine := counter + " " + styleFileName.Render(label)
	row1 := padBetween(nameLine, res, w)
	b.WriteString(row1 + "\n")

	// Row 2: progress bar + pct + elapsed + eta
	// Build the right-hand side first so we can measure its true rendered
	// width and give the bar exactly the remaining columns.
	pctStr := stylePct.Render(fmt.Sprintf("%d%%", js.pct))
	elapsedStr := styleElapsed.Render("elapsed " + fmtDuration(m.jobElapsed(js)))
	etaStr := styleETA.Render("ETA " + m.jobETA(js))
	right := pctStr + "  " + elapsedStr + "  " + etaStr
	const sep = "  " // gap between bar and right side
	barWidth := w - lipgloss.Width(right) - len(sep)
	if barWidth < 10 {
		barWidth = 10
	}
	bar := renderBar(js.pct, barWidth)
	row2 := bar + sep + right
	// Clamp to width (safety net for very narrow terminals)
	row2 = truncateANSI(row2, w)
	b.WriteString(row2 + "\n\n")

	return b.String()
}

func (m model) renderLogLine(ll logLine, w int) string {
	text := truncate(ll.text, w)
	switch ll.kind {
	case kindKept:
		return styleLogOK.Render(text)
	case kindDiscard:
		return styleLogDiscard.Render(text)
	case kindError:
		return styleLogErr.Render(text)
	case kindSummary:
		return lipgloss.NewStyle().Foreground(colorHeading).Render(text)
	default:
		return styleLogInfo.Render(text)
	}
}

func (m model) renderLogContent() string {
	w := m.innerWidth()
	var lines []string
	for _, ll := range m.logLines {
		lines = append(lines, m.renderLogLine(ll, w))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderSectionSep(label string, w int) string {
	lbl := styleSectionLabel.Render(" " + label + " ")
	lblWidth := lipgloss.Width(lbl)
	lineWidth := w - lblWidth - 1
	if lineWidth < 0 {
		lineWidth = 0
	}
	return styleMuted.Render("─") + lbl + styleMuted.Render(strings.Repeat("─", lineWidth))
}

// innerWidth is the usable width inside the border.
func (m model) innerWidth() int {
	w := m.width - 4 // 2 border + 2 padding
	if w < 20 {
		w = 20
	}
	return w
}

func (m model) logHeight() int {
	// title(1) + controlBar(1) + sep(1) + blank before jobs(1 if any) + jobs*3 + border/padding(4)
	used := 3 + len(m.jobs)*3
	if len(m.jobs) > 0 {
		used++ // blank line before first job
	}
	h := m.height - 4 - used
	if h < 3 {
		h = 3
	}
	return h
}

func (m model) logViewportWidth() int  { return m.innerWidth() }
func (m model) logViewportHeight() int { return m.logHeight() }

func (m model) lastLogLines(n int) []logLine {
	if len(m.logLines) <= n {
		return m.logLines
	}
	return m.logLines[len(m.logLines)-n:]
}

// clampPct bounds a progress percentage to [0, 100]. FFmpeg can report more
// than 100% when the probed duration is slightly short, which would otherwise
// show "103%" and produce a negative ETA.
func clampPct(p int) int {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// renderBar renders a Unicode block progress bar of the given width.
func renderBar(pct, width int) string {
	if width <= 0 {
		return ""
	}
	filled := width * pct / 100
	if filled > width {
		filled = width
	}
	fill := lipgloss.NewStyle().Foreground(colorFill).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Foreground(colorEmpty).Render(strings.Repeat("░", width-filled))
	return fill + empty
}

// padBetween returns a string of exactly width runes with left on the left and
// right on the right, filled with spaces in between.
func padBetween(left, right string, width int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := width - lw - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// truncate clips s to maxLen terminal cells (no ANSI awareness needed here
// since we apply styles after truncation). It counts display width rather than
// runes so wide characters (CJK, emoji) cannot overflow the line.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return cutWidth(s, maxLen)
	}
	return cutWidth(s, maxLen-3) + "..."
}

// cutWidth returns the longest prefix of s that fits in w terminal cells.
func cutWidth(s string, w int) string {
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if cur+rw > w {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String()
}

// truncateANSI truncates an ANSI-styled string to at most maxLen visible
// columns. Iterates from the tail since ANSI escape sequences make byte-length
// unreliable; lipgloss.Width gives the true visible column count.
func truncateANSI(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > maxLen {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

// ── UI (thread-safe façade) ───────────────────────────────────────────────────

// UI wraps a Bubble Tea program and exposes a thread-safe API for worker
// goroutines to drive the display.
type UI struct {
	prog       *tea.Program
	nextID     atomic.Uint64
	mu         sync.Mutex
	logW       *tuiWriter
	timerStart time.Time
	// plain mode: used when stdout is not a TTY
	plain  bool
	plainW io.Writer
}

// New creates and starts a Bubble Tea UI writing to stdout.
// When stdout is not a TTY (e.g. redirected to a file), the TUI is skipped
// and a plain-text fallback is used instead.
//
// opts describes which setup questions to ask before conversion begins.
// The returned channel receives exactly one SetupAnswers value (then closes)
// when the user finishes the setup phase.  If setup is entirely skipped (all
// flags pre-filled), the channel is closed immediately with zero-value answers.
//
// onControl is called (from the Bubble Tea goroutine) when the user presses
// p/s/q during conversion. It must be non-blocking. Pass nil to disable key handling.
//
// Call [UI.Wait] (or defer it) to block until the program exits.
func New(opts SetupOptions, onControl func(action ControlAction)) (*UI, <-chan SetupAnswers) {
	answersCh := make(chan SetupAnswers, 1)

	// If stdout is not a terminal, skip Bubble Tea entirely — it requires a TTY.
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		// Deliver zero-value answers (NOT Cancelled) so callers proceed using
		// their CLI flags/config; a missing input path is reported by the caller.
		go func() {
			answersCh <- SetupAnswers{}
			close(answersCh)
		}()
		return NewPlain(os.Stdout), answersCh
	}

	// screenReady is closed as soon as the alt-screen gets its first
	// WindowSizeMsg.  convertReady is closed when the converting phase is
	// initialised (may be much later when setup phase is active).
	screenReady := make(chan struct{})
	convertReady := make(chan struct{})

	m := initialModel(opts, answersCh)
	m.onReady = func() {
		select {
		case <-convertReady:
		default:
			close(convertReady)
		}
	}
	m.onScreenReady = func() {
		select {
		case <-screenReady:
		default:
			close(screenReady)
		}
	}
	if onControl != nil {
		m.onControl = func(a controlAction) { onControl(ControlAction(a)) }
	}
	prog := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	w := &tuiWriter{prog: prog}
	u := &UI{prog: prog, logW: w}
	go func() { prog.Run() }() //nolint:errcheck

	// Wait for the alt-screen to be ready (first WindowSizeMsg).
	// When setup is active the caller then waits on answersCh independently;
	// when setup is skipped we additionally wait for convertReady.
	select {
	case <-screenReady:
	case <-time.After(2 * time.Second):
	}
	if m.phase == phaseConverting {
		// No setup — also wait for the viewport to initialise.
		select {
		case <-convertReady:
		case <-time.After(2 * time.Second):
		}
	}
	return u, answersCh
}

// NewPlain returns a UI that writes plain text to w instead of driving a
// Bubble Tea program. It is what New uses when stdout is not a terminal, and it
// lets callers (and tests) run the conversion code without a TTY.
func NewPlain(w io.Writer) *UI {
	return &UI{plain: true, plainW: w, logW: &tuiWriter{plain: true, plainW: w}}
}

// ControlAction mirrors the internal controlAction for external callers.
type ControlAction int

const (
	// ActionPause requests the pipeline to pause/resume.
	ActionPause ControlAction = ControlAction(actionPause)
	// ActionStopAfterCurrent requests a graceful stop after the current job finishes.
	ActionStopAfterCurrent ControlAction = ControlAction(actionStopAfterCurrent)
	// ActionStopNow requests an immediate pipeline abort.
	ActionStopNow ControlAction = ControlAction(actionStopNow)
)

// SendControlState pushes updated pause/stop state into the TUI so the
// status bar reflects the current reality.
func (u *UI) SendControlState(paused bool, stopping StopKind) {
	if u.plain {
		return
	}
	u.prog.Send(msgControlState{paused: paused, stopping: stopping})
}

// Wait shuts down the Bubble Tea program and waits for it to finish.
// Call this before the process exits (typically via defer).
func (u *UI) Wait() {
	if u.plain {
		return
	}
	u.prog.Quit()
	// small grace period so the final frame is flushed
	time.Sleep(100 * time.Millisecond)
}

// ShowSummary appends the summary lines to the TUI log viewport, switches the
// control bar to "press Enter to exit" mode, and blocks until the user quits
// (Enter / q / ctrl+c) or the program exits for any other reason.
//
// Call this instead of Wait() when you want the user to read the summary before
// the alt-screen is torn down.  After ShowSummary returns the alt-screen has
// been restored and it is safe to print to stdout normally.
func (u *UI) ShowSummary(lines []string) {
	if u.plain {
		// Plain mode: just print the lines directly.
		for _, l := range lines {
			fmt.Fprintln(u.plainW, l)
		}
		return
	}
	u.prog.Send(msgShowSummary{lines: lines})
	// prog.Run() returns when the program quits (user pressed Enter/q or
	// tea.Quit was sent).  We already launched it in a goroutine in New(), so
	// we just wait for that goroutine via the program's built-in wait.
	u.prog.Wait()
}

// StartTimer signals that work has begun — starts the global elapsed timer.
func (u *UI) StartTimer() {
	u.timerStart = time.Now()
	if u.plain {
		return
	}
	u.prog.Send(msgStartTimer{})
}

// SetConversionStats informs the TUI of the total file count and total byte
// size for all files that will be processed, enabling a global ETA estimate.
func (u *UI) SetConversionStats(totalFiles int, totalBytes int64) {
	if u.plain {
		return
	}
	u.prog.Send(msgSetConversionStats{totalFiles: totalFiles, totalBytes: totalBytes})
}

// FileFinished notifies the TUI that one file has finished processing
// (converted, skipped, or errored), updating the global ETA.
// sizeBytes should be the original file size (0 if unknown).
func (u *UI) FileFinished(sizeBytes int64) {
	if u.plain {
		return
	}
	u.prog.Send(msgFileFinished{sizeBytes: sizeBytes})
}

// Elapsed returns the wall-clock duration since StartTimer was called.
// Returns 0 if StartTimer was never called.
func (u *UI) Elapsed() time.Duration {
	if u.timerStart.IsZero() {
		return 0
	}
	return time.Since(u.timerStart).Round(time.Second)
}

// StartJob registers a new conversion job and returns an opaque job ID.
// fileNum/totalFiles are used for the "[N/M]" counter.
// heightP is the video vertical resolution (0 to omit).
// totalSeconds is the video duration (used for ETA; 0 = unknown).
func (u *UI) StartJob(label string, fileNum, totalFiles, heightP int, totalSeconds float64) uint64 {
	id := u.nextID.Add(1)
	if u.plain {
		fmt.Fprintf(u.plainW, "[%d/%d] Converting: %s (%dp)\n", fileNum, totalFiles, label, heightP)
		return id
	}
	u.prog.Send(msgJobStart{
		id:           id,
		label:        label,
		fileNum:      fileNum,
		totalFiles:   totalFiles,
		heightP:      heightP,
		totalSeconds: totalSeconds,
	})
	return id
}

// UpdateProgress updates the progress for a running job (0–100).
// Thread-safe; can be called from any goroutine.
func (u *UI) UpdateProgress(id uint64, pct int) {
	if u.plain {
		return // no progress output in plain mode
	}
	u.prog.Send(msgJobProgress{id: id, pct: pct})
}

// CompleteJob removes the active bar and appends a completion summary line.
func (u *UI) CompleteJob(id uint64, summary string, kind completionKind) {
	if u.plain {
		fmt.Fprintln(u.plainW, summary)
		return
	}
	u.prog.Send(msgJobComplete{id: id, summary: summary, kind: kind})
}

// CompleteKept is a helper for kept files.
func (u *UI) CompleteKept(id uint64, summary string) {
	u.CompleteJob(id, summary, kindKept)
}

// CompleteDiscard is a helper for discarded files.
func (u *UI) CompleteDiscard(id uint64, summary string) {
	u.CompleteJob(id, summary, kindDiscard)
}

// CompleteError is a helper for errored files.
func (u *UI) CompleteError(id uint64, summary string) {
	u.CompleteJob(id, summary, kindError)
}

// Log appends an informational line to the log area.
func (u *UI) Log(text string) {
	if u.plain {
		fmt.Fprintln(u.plainW, text)
		return
	}
	u.prog.Send(msgLog{text: text})
}

// Writer returns an io.Writer that routes lines into the TUI log panel.
// Suitable for use as the logrus output writer.
func (u *UI) Writer() io.Writer {
	return u.logW
}

// PrintSummary writes the final summary block directly (after TUI exits).
// Call this AFTER Wait() so the alternate screen has been restored.
func (u *UI) PrintSummary(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}

// ── tuiWriter ─────────────────────────────────────────────────────────────────

// tuiWriter adapts io.Writer to route log lines into the TUI via Send.
type tuiWriter struct {
	prog *tea.Program
	buf  strings.Builder
	mu   sync.Mutex
	// plain mode: write directly instead of sending to Bubble Tea
	plain  bool
	plainW io.Writer
}

// fileOnlyPrefix is a sentinel prepended to log messages that should be written
// to the log file but NOT displayed in the TUI viewport.  tuiWriter strips the
// prefix before forwarding and silently drops the line from the TUI.
const fileOnlyPrefix = "\x00FILEONLY\x00"

// FileOnlyPrefix is the exported sentinel for use by other packages.
// Prepend it to a logrus message to have it written to the log file only,
// bypassing the TUI viewport.
const FileOnlyPrefix = fileOnlyPrefix

func (w *tuiWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.plain {
		// In plain mode strip the sentinel before writing.
		out := strings.ReplaceAll(string(p), fileOnlyPrefix, "")
		if out == "" || out == "\n" {
			return len(p), nil
		}
		_, err = w.plainW.Write([]byte(out))
		return len(p), err
	}
	w.buf.Write(p)
	// Flush complete lines
	for {
		s := w.buf.String()
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(s[:idx], "\r")
		w.buf.Reset()
		w.buf.WriteString(s[idx+1:])
		if line != "" && !strings.Contains(line, fileOnlyPrefix) {
			w.prog.Send(msgLog{text: line})
		}
	}
	return len(p), nil
}
