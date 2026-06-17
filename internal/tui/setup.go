package tui

// setup.go implements the interactive setup phase that runs inside the same
// Bubble Tea program as the conversion TUI.
//
// Flow:
//   1. (optional) folder picker  — skipped when path already provided via CLI
//   2. Series of yes/no and numeric questions
//   3. msgSetupDone fires → parent model transitions to phaseConverting
//
// All questions whose answers were already supplied via CLI flags are skipped
// automatically; the phase is skipped entirely when nothing is needed.

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ydakilux/reforge/internal/fileutil"
)

// ── Startup phase messages ────────────────────────────────────────────────────

// msgStartupLine is sent when a new status line arrives from StartupCh, or
// when the channel closes (done == true).
type msgStartupLine struct {
	text string
	done bool
}

// waitStartupLine returns a Cmd that reads the next item from ch.
func waitStartupLine(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-ch
		if !ok {
			return msgStartupLine{done: true}
		}
		return msgStartupLine{text: text}
	}
}

// ── Public types ──────────────────────────────────────────────────────────────

// DriveInfo describes one available output drive.
type DriveInfo struct {
	Root      string // e.g. "D:\" on Windows, "/mnt/d/" on Unix
	Label     string // display string, e.g. "D:\ (205.5 GB free / 931.5 GB total)"
	FreeBytes int64  // available bytes (0 = unknown)
}

// SetupOptions describes which questions the setup phase must ask.
// Fields set to their zero value mean "already known — skip this question".
type SetupOptions struct {
	// StartupCh, when non-nil, drives a "startup" screen shown before any
	// questions.  The app sends progress lines (e.g. "Detecting GPU…"); closing
	// the channel signals that startup work is complete and questions can begin.
	StartupCh <-chan string

	// NeedFolder: true when no path was given on the CLI.
	NeedFolder bool
	// StartDir is the initial directory for the folder picker (empty = home).
	StartDir string
	// VideoExtensions is the list of extensions used to count video files in
	// the confirmation preview (e.g. []string{".mp4", ".mkv"}).
	// Case-insensitive.  Empty = count all files.
	VideoExtensions []string

	// Each "Need*" flag = true means the question must be asked.
	// The corresponding "Default*" value is shown as the pre-filled suggestion.
	NeedBypass       bool
	NeedForceHevc    bool
	NeedParallelJobs bool
	DefaultParallel  int
	NeedOutputDrive  bool
	AvailableDrives  []DriveInfo // populated only when NeedOutputDrive == true
	// TotalFileSizeBytes is the total size of discovered video files.
	// When non-zero it is shown in the output-drive selection step so the
	// user knows how much space is needed on the target drive.
	TotalFileSizeBytes int64
	// BytesPerDrive maps each source drive root to the total bytes of matching
	// video files on that drive. Used by the destination-full warning when the
	// user keeps the source drive(s) as output. May be nil when sources come
	// from the interactive folder picker — in that case the scan populates the
	// equivalent map at confirm time.
	BytesPerDrive map[string]int64
}

// SetupAnswers carries the values collected during setup.
type SetupAnswers struct {
	Paths        []string // selected folders/files; nil when NeedFolder was false
	Bypass       bool
	ForceHevc    bool
	ParallelJobs int    // 0 when NeedParallelJobs was false
	OutputDrive  string // "" = same drive
	Cancelled    bool   // user pressed Esc/q before completing
}

// msgSetupDone is sent by the setup sub-model when all answers are collected.
type msgSetupDone struct{ answers SetupAnswers }

// ── Setup sub-model ───────────────────────────────────────────────────────────

type setupStep int

const (
	stepStartup setupStep = iota // waiting for startup goroutine to finish
	stepFolder                   // directory picker (multi-select)
	stepConfirm                  // preview: dir/file counts before proceeding
	stepBypass
	stepForceHevc
	stepParallelJobs
	stepOutputDrive
	stepDriveSpaceWarn
	stepDone
)

// confirmScan holds the pre-scanned overview shown in stepConfirm.
type confirmScan struct {
	totalDirs      int
	totalFiles     int
	totalSizeBytes int64            // sum of sizes of matching video files
	baseDirs       []string         // unique root paths the user added
	bytesPerDrive  map[string]int64 // total matching bytes grouped by drive root
}

type setupModel struct {
	opts    SetupOptions
	answers SetupAnswers

	step setupStep

	// startup status lines (stepStartup)
	startupLines []string
	startupDone  bool
	startupTick  int // spinner frame counter

	// folder picker (stepFolder) — multi-select
	fp            filepicker.Model
	selectedPaths []string      // ordered list of confirmed selections
	fpLastDir     string        // last CurrentDirectory shown in the hint bar
	fpFiles       []os.DirEntry // mirror of fp's private files slice
	fpCursor      int           // mirror of fp's private selected index
	fpMin         int           // mirror of fp's private min (first visible index)

	// drive-switcher overlay (shown inside stepFolder via Tab)
	fpDriveOverlay bool
	fpDriveCursor  int

	// goto-directory overlay (shown inside stepFolder via Ctrl+G)
	fpGotoOverlay bool
	fpGotoInput   textinput.Model

	// stepConfirm
	scan confirmScan

	// text input (stepParallelJobs)
	ti textinput.Model

	// drive selection cursor (stepOutputDrive)
	driveCursor int
	// whether user said yes to "different drive?" before showing drive list
	driveYesAsked  bool
	driveWantsDiff bool

	// stepDriveSpaceWarn — populated when we detect the destination(s) may run
	// out of space. Empty slice means no warning is needed and the step is
	// skipped automatically.
	spaceRisks []driveSpaceRisk

	width  int
	height int
}

// driveSpaceRisk describes a destination drive whose free space is less than
// the worst-case bytes that will be written to it.
type driveSpaceRisk struct {
	root      string // e.g. "D:\"
	freeBytes int64  // available bytes on the drive
	needBytes int64  // worst-case bytes to be written (= source bytes routed here)
}

func newSetupModel(opts SetupOptions) setupModel {
	// Build text input for parallel jobs.
	ti := textinput.New()
	ti.CharLimit = 2
	ti.Width = 4
	if opts.DefaultParallel > 0 {
		ti.Placeholder = strconv.Itoa(opts.DefaultParallel)
	} else {
		ti.Placeholder = "1"
	}

	// Build filepicker.
	startDir := opts.StartDir
	if startDir == "" {
		// Prefer USERPROFILE on Windows; fall back to os.UserHomeDir().
		if h := os.Getenv("USERPROFILE"); h != "" {
			startDir = h
		} else if h, err := os.UserHomeDir(); err == nil {
			startDir = h
		} else {
			startDir = "."
		}
	}
	fp := filepicker.New()
	fp.CurrentDirectory = startDir
	fp.DirAllowed = true
	fp.FileAllowed = true
	fp.AutoHeight = false // must be false; Height set explicitly below
	fp.Height = 12        // safe default before first WindowSizeMsg
	fp.ShowSize = false
	fp.ShowPermissions = false
	fp.ShowHidden = false
	fp.KeyMap.Select.SetKeys(" ")                           // Space = confirm dir; Enter = navigate in
	fp.KeyMap.Back.SetKeys("h", "backspace", "left", "esc") // keep esc for Back (not cancel)

	gotoInput := textinput.New()
	gotoInput.CharLimit = 260
	gotoInput.Width = 60
	gotoInput.Placeholder = startDir

	m := setupModel{opts: opts, fp: fp, ti: ti, fpGotoInput: gotoInput}
	m.fpLoadDir(startDir)

	// If a startup channel is provided, always begin at stepStartup.
	if opts.StartupCh != nil {
		m.step = stepStartup
	} else {
		m.startupDone = true
		m.step = m.firstStep()
		if m.step == stepParallelJobs {
			m.ti.Focus()
		}
	}
	return m
}

// firstStep returns the first question step that needs user input.
func (m *setupModel) firstStep() setupStep {
	if m.opts.NeedFolder {
		return stepFolder
	}
	return m.nextStepAfter(stepFolder)
}

// nextStepAfter returns the next step that needs user input after s.
func (m *setupModel) nextStepAfter(s setupStep) setupStep {
	for next := s + 1; next < stepDone; next++ {
		switch next {
		case stepStartup:
			// never returned — startup is entered directly, not via advance()
		case stepFolder:
			if m.opts.NeedFolder {
				return next
			}
		case stepConfirm:
			if m.opts.NeedFolder {
				return next
			}
		case stepBypass:
			if m.opts.NeedBypass {
				return next
			}
		case stepForceHevc:
			if m.opts.NeedForceHevc {
				return next
			}
		case stepParallelJobs:
			if m.opts.NeedParallelJobs {
				return next
			}
		case stepOutputDrive:
			if m.opts.NeedOutputDrive {
				return next
			}
		case stepDriveSpaceWarn:
			if len(m.spaceRisks) > 0 {
				return next
			}
		}
	}
	return stepDone
}

func (m setupModel) init() tea.Cmd {
	if m.step == stepStartup && m.opts.StartupCh != nil {
		return waitStartupLine(m.opts.StartupCh)
	}
	if m.step == stepFolder {
		// Height is set in newSetupModel (safe default = 12); WindowSizeMsg
		// will update it once the terminal size is known.
		return m.fp.Init()
	}
	return nil
}

func (m setupModel) update(msg tea.Msg) (setupModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncFpHeight()

	case msgStartupLine:
		if msg.done {
			// Startup goroutine finished — advance to questions.
			m.startupDone = true
			next := m.firstStep()
			m.step = next
			var cmd tea.Cmd
			if next == stepFolder {
				// Apply known terminal height before Init() loads the directory,
				// so m.fp.max is computed correctly from a non-zero Height.
				m.syncFpHeight()
				m.fpLoadDir(m.fp.CurrentDirectory)
				cmd = m.fp.Init()
			} else if next == stepParallelJobs {
				m.ti.Focus()
			}
			return m, cmd
		}
		// Append the new status line and keep listening.
		m.startupLines = append(m.startupLines, msg.text)
		m.startupTick++
		return m, waitStartupLine(m.opts.StartupCh)

	case tea.KeyMsg:
		switch m.step {
		case stepStartup:
			return m.updateStepStartup(msg)
		case stepFolder:
			return m.updateStepFolder(msg)
		case stepConfirm:
			return m.updateStepConfirm(msg)
		case stepBypass, stepForceHevc:
			return m.updateStepYesNo(msg)
		case stepParallelJobs:
			return m.updateStepParallelJobs(msg)
		case stepOutputDrive:
			return m.updateStepOutputDrive(msg)
		case stepDriveSpaceWarn:
			return m.updateStepDriveSpaceWarn(msg)
		}
	}

	// Delegate non-key messages to sub-components.
	if m.step == stepFolder {
		return m.updateStepFolder(msg)
	}

	return m, nil
}

// syncFpHeight recalculates m.fp.Height so that the filepicker + the
// selected-paths list below it always fit within the terminal window.
// Call this whenever m.selectedPaths or m.height changes.
func (m *setupModel) syncFpHeight() {
	fpH, _ := m.splitHeights()
	m.fp.Height = fpH
}

// splitHeights returns (fpHeight, selHeight) — the number of rows to give the
// filepicker and the selection list respectively, so that everything fits
// within m.height.
func (m *setupModel) splitHeights() (fpHeight, selHeight int) {
	// Measure actual chrome lines by rendering them. This avoids hard-coding
	// a constant that silently drifts when the view layout changes.
	chrome := m.chromeLineCount()
	available := m.height - chrome
	if available < 6 {
		available = 6
	}

	n := len(m.selectedPaths)
	if n == 0 {
		return available, 0
	}

	maxSel := available * 40 / 100
	if maxSel < 1 {
		maxSel = 1
	}
	if maxSel > 8 {
		maxSel = 8
	}
	selH := n
	if selH > maxSel {
		selH = maxSel
	}

	fpH := available - selH
	if fpH < 3 {
		fpH = 3
	}
	return fpH, selH
}

// chromeLineCount returns the number of lines consumed by everything other than
// the filepicker list and the selection items: title, label, hints, current
// directory, border, and selection header (if any).
func (m *setupModel) chromeLineCount() int {
	w := m.width - 2
	if w < 50 {
		w = 50
	}

	// Border top + bottom.
	lines := 2

	// Title ("Video Converter — Setup") + blank line.
	lines += 2

	// "Select input folder"
	lines++

	// Hint bar — may wrap to multiple lines on narrow terminals.
	tabHint := ""
	if len(m.opts.AvailableDrives) > 0 {
		tabHint = "  [Tab] switch drive"
	}
	hint := "[Space] toggle highlighted  [←/h] up  [→/l/Enter] open  [d] remove last  [q] cancel" + tabHint + "  [Ctrl+G] go to path"
	hintWidth := lipgloss.Width(hint)
	innerW := w - 4
	if innerW < 1 {
		innerW = 1
	}
	hintLines := (hintWidth + innerW - 1) / innerW
	if hintLines < 1 {
		hintLines = 1
	}
	lines += hintLines

	// Current directory + blank line before filepicker.
	lines += 2

	// Selection pane header (blank + label) when selections exist.
	if len(m.selectedPaths) > 0 {
		lines += 2
	}

	return lines
}

// advance moves to the next needed step.
func (m *setupModel) advance() {
	prev := m.step
	m.step = m.nextStepAfter(m.step)

	// Compute destination space risks at the moment the output-drive choice
	// is finalised. This is either when leaving stepOutputDrive itself, or
	// when the output-drive step is skipped entirely (e.g. --same-drive on
	// the CLI) and we cross over it during a transition. Recomputing here —
	// rather than earlier — ensures the warning reflects the drive the user
	// just picked, even when revisiting the choice after a "back".
	crossedOutputDrive := prev <= stepOutputDrive && m.step > stepOutputDrive
	if crossedOutputDrive {
		m.spaceRisks = m.computeSpaceRisks(m.answers.OutputDrive)
		// nextStepAfter ran before we populated spaceRisks, so it may have
		// skipped stepDriveSpaceWarn. Re-evaluate: if risks now exist and the
		// previous decision landed past the warn step, redirect there.
		if len(m.spaceRisks) > 0 && m.step > stepDriveSpaceWarn {
			m.step = stepDriveSpaceWarn
		}
	}

	if m.step == stepParallelJobs {
		m.ti.Focus()
	}
}

// advanceAndReturn moves to the next needed step and returns the updated model
// with no command.  Convenience for the common advance-then-return pattern.
func (m setupModel) advanceAndReturn() (setupModel, tea.Cmd) {
	m.advance()
	return m, nil
}

// jumpToDrive teleports the filepicker to the drive selected in the overlay,
// closes the overlay, and reloads the directory listing.
func (m *setupModel) jumpToDrive() tea.Cmd {
	m.fpDriveOverlay = false
	if len(m.opts.AvailableDrives) == 0 {
		return nil
	}
	root := m.opts.AvailableDrives[m.fpDriveCursor].Root
	m.fp.CurrentDirectory = root
	// Reset scroll/selection stacks so the new directory starts at the top.
	m.fp = resetFilepickerView(m.fp)
	m.fpLoadDir(root)
	return m.fp.Init()
}

// resetFilepickerView zeroes the visible-window state of a filepicker so that
// after a directory jump the list starts at the top.
func resetFilepickerView(fp filepicker.Model) filepicker.Model {
	// Re-create a fresh model copying all config fields, preserving Height.
	fresh := filepicker.New()
	fresh.CurrentDirectory = fp.CurrentDirectory
	fresh.DirAllowed = fp.DirAllowed
	fresh.FileAllowed = fp.FileAllowed
	fresh.AutoHeight = fp.AutoHeight
	fresh.Height = fp.Height
	fresh.ShowSize = fp.ShowSize
	fresh.ShowPermissions = fp.ShowPermissions
	fresh.ShowHidden = fp.ShowHidden
	fresh.KeyMap = fp.KeyMap
	fresh.Styles = fp.Styles
	return fresh
}

// fpLoadDir reads dir synchronously and stores the (sorted, hidden-filtered)
// entries in m.fpFiles, resetting m.fpCursor to 0.  Mirrors what the
// filepicker does internally so we always know which entry is highlighted.
func (m *setupModel) fpLoadDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		m.fpFiles = nil
		m.fpCursor = 0
		return
	}
	// Sort: dirs first, then alphabetical — same order as filepicker.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() == entries[j].IsDir() {
			return entries[i].Name() < entries[j].Name()
		}
		return entries[i].IsDir()
	})
	// Filter hidden files (same logic as filepicker when ShowHidden=false).
	if !m.fp.ShowHidden {
		var visible []os.DirEntry
		for _, e := range entries {
			hidden, _ := filepicker.IsHidden(e.Name())
			if !hidden {
				visible = append(visible, e)
			}
		}
		entries = visible
	}
	m.fpFiles = entries
	m.fpCursor = 0
	m.fpMin = 0
}

// togglePath adds path to the slice if absent, or removes it if present.
func togglePath(paths []string, path string) []string {
	for i, p := range paths {
		if strings.EqualFold(p, path) {
			return append(paths[:i], paths[i+1:]...)
		}
	}
	return append(paths, path)
}

// scanPaths walks all selected paths and counts directories and matching files.
// totalDirs counts each selected root dir plus every subdirectory inside them.
func scanPaths(paths []string, extensions []string) confirmScan {
	extSet := make(map[string]bool, len(extensions))
	for _, e := range extensions {
		extSet[strings.ToLower(e)] = true
	}
	matchAll := len(extSet) == 0

	seen := make(map[string]bool)
	sc := confirmScan{bytesPerDrive: make(map[string]int64)}

	for _, root := range paths {
		root = filepath.Clean(root)
		if seen[root] {
			continue
		}
		seen[root] = true
		sc.baseDirs = append(sc.baseDirs, root)

		info, err := os.Stat(root)
		if err != nil {
			continue
		}

		if !info.IsDir() {
			// Single file selected.
			ext := strings.ToLower(filepath.Ext(root))
			if matchAll || extSet[ext] {
				sc.totalFiles++
				sc.totalSizeBytes += info.Size()
				sc.bytesPerDrive[fileutil.GetDriveRoot(root)] += info.Size()
			}
			continue
		}

		// Count the root dir itself.
		sc.totalDirs++

		filepath.Walk(root, func(p string, fi os.FileInfo, err error) error { //nolint:errcheck
			if err != nil || fi == nil {
				return nil
			}
			if fi.IsDir() {
				if p != root {
					sc.totalDirs++
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if matchAll || extSet[ext] {
				sc.totalFiles++
				sc.totalSizeBytes += fi.Size()
				sc.bytesPerDrive[fileutil.GetDriveRoot(p)] += fi.Size()
			}
			return nil
		})
	}
	return sc
}

// done returns true when setup has completed (all answers collected).
func (m *setupModel) done() bool { return m.step == stepDone }

// ── Setup view ────────────────────────────────────────────────────────────────

var (
	setupStyleTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
	setupStyleLabel   = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).Bold(true)
	setupStyleHint    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	setupStyleAnswer  = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Bold(true)
	setupStyleCurrent = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))
	setupStyleBorder  = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#4B5563"))
	setupStyleCursor            = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
	setupStyleDriveNormal       = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB"))
	setupStyleDriveSelected     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
	setupStyleDriveEnough       = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E"))            // green — enough free space
	setupStyleDriveInsufficient = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))            // red — not enough space
	setupStyleWarn              = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Bold(true) // amber — warning
	setupStyleStartupLine       = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB"))
	setupStyleStartupDim        = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	setupStyleSpinner           = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m setupModel) view() string {
	w := m.width - 2
	if w < 50 {
		w = 50
	}

	var b strings.Builder

	// Title
	b.WriteString(setupStyleTitle.Render("Video Converter  —  Setup") + "\n\n")

	switch m.step {
	case stepStartup:
		b.WriteString(m.viewStepStartup())
	case stepFolder:
		b.WriteString(m.viewStepFolder(w))
	case stepConfirm:
		b.WriteString(m.viewStepConfirm(w))
	case stepBypass, stepForceHevc:
		b.WriteString(m.viewStepYesNo())
	case stepParallelJobs:
		b.WriteString(m.viewStepParallelJobs())
	case stepOutputDrive:
		b.WriteString(m.viewStepOutputDrive(w))
	case stepDriveSpaceWarn:
		b.WriteString(m.viewStepDriveSpaceWarn(w))
	}

	return setupStyleBorder.Width(w).MaxHeight(m.height).Render(b.String()) + "\n"
}
