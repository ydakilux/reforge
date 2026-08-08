// Package app — launcher.go
//
// When reforge is invoked with no CLI arguments at all, main.go calls
// RunLauncher() which displays an interactive bubbletea menu that lets the
// user either:
//
//   - Start a conversion (which falls through to the normal conversion flow)
//   - View any of the stats/reports normally accessible as subcommands
//   - Open the HTML dashboard in a browser
//   - Quit
//
// The menu loops until the user picks "Start conversion" or "Quit". Each
// stats view is rendered into a scrollable viewport; pressing any key
// returns to the menu.

package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ydakilux/reforge/internal/dashboard"
	"github.com/ydakilux/reforge/internal/database"
)

// LauncherResult tells main.go what to do after the launcher exits.
type LauncherResult int

const (
	// LauncherQuit means the user picked Quit (or pressed Esc/Ctrl+C).
	LauncherQuit LauncherResult = iota
	// LauncherStartConversion means the user picked "Start conversion" and the
	// caller should fall through to the regular conversion flow.
	LauncherStartConversion
	// launcherOpenRunsBrowser is an internal signal: the user picked "Recent
	// runs" and RunLauncher should launch the runs browser, then re-enter
	// the menu loop. Never returned from RunLauncher.
	launcherOpenRunsBrowser
)

// RunLauncher displays the interactive menu and returns the user's choice.
// It opens the database read-only for rendering stats; failure to open the
// database is non-fatal (stats actions will display the error in the viewport).
func RunLauncher() LauncherResult {
	// IMPORTANT: NewSQLiteStore returns a concrete *SQLiteStore. Assigning
	// it to a database.Store interface variable would create a typed-nil
	// interface (interface value non-nil, concrete pointer nil) on error,
	// which then crashes any later `if store != nil { store.Foo() }` check.
	// Open via the concrete type first, then promote to the interface only
	// after we know the pointer is non-nil.
	concrete, openErr := database.NewSQLiteStore(defaultDBPath(), subcommandLogger())
	var (
		store    database.Store
		storeErr string
	)
	if openErr != nil {
		storeErr = openErr.Error()
	} else if concrete != nil {
		store = concrete
		defer concrete.Close()
	}

	for {
		m := newLauncherModel(store)
		m.storeErr = storeErr
		p := tea.NewProgram(m, tea.WithAltScreen())
		final, err := p.Run()
		if err != nil {
			fmt.Println("launcher error:", err)
			return LauncherQuit
		}
		fm, ok := final.(launcherModel)
		if !ok {
			return LauncherQuit
		}
		switch fm.result {
		case launcherOpenRunsBrowser:
			// Hand off to the runs browser, then loop back into the menu.
			if runRunsBrowser(store) == runsBrowserQuit {
				return LauncherQuit
			}
			continue
		case LauncherStartConversion:
			return LauncherStartConversion
		default:
			return LauncherQuit
		}
	}
}

// ── Model ────────────────────────────────────────────────────────────────────

type launcherState int

const (
	launcherStateMenu launcherState = iota
	launcherStateView
)

type launcherAction int

const (
	actionStartConversion launcherAction = iota
	actionStats
	actionRecent
	actionErrors
	actionNotBeneficial
	actionFormats
	actionSpaceSaved
	actionDashboard
	actionQuit
)

type launcherChoice struct {
	label  string
	hint   string
	action launcherAction
}

var launcherChoices = []launcherChoice{
	{"Start conversion", "Pick folders and convert videos to HEVC", actionStartConversion},
	{"Stats", "Aggregate conversion statistics", actionStats},
	{"Recent runs", "Browse previous conversion runs", actionRecent},
	{"Errors", "List failed conversions", actionErrors},
	{"Not beneficial", "Conversions where the output was larger", actionNotBeneficial},
	{"Formats", "Source-format breakdown", actionFormats},
	{"Space saved", "Total space saved (all time)", actionSpaceSaved},
	{"Dashboard", "Generate HTML dashboard and open in browser", actionDashboard},
	{"Quit", "Exit reforge", actionQuit},
}

type launcherModel struct {
	store    database.Store
	storeErr string // populated when NewSQLiteStore failed; shown in report views
	state    launcherState
	cursor   int

	// stateView
	vp        viewport.Model
	viewTitle string
	viewReady bool

	width  int
	height int

	result LauncherResult
}

func newLauncherModel(store database.Store) launcherModel {
	return launcherModel{
		store:  store,
		state:  launcherStateMenu,
		result: LauncherQuit,
	}
}

func (m launcherModel) Init() tea.Cmd { return nil }

func (m launcherModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpW, vpH := m.viewportSize()
		if !m.viewReady {
			m.vp = viewport.New(vpW, vpH)
			m.viewReady = true
		} else {
			m.vp.Width = vpW
			m.vp.Height = vpH
		}

	case tea.KeyMsg:
		switch m.state {
		case launcherStateMenu:
			return m.updateMenu(msg)
		case launcherStateView:
			return m.updateView(msg)
		}
	}
	return m, nil
}

func (m launcherModel) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(launcherChoices)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(launcherChoices) - 1
	case "ctrl+c", "q", "esc":
		m.result = LauncherQuit
		return m, tea.Quit
	case "enter", " ":
		return m.activate()
	}
	// Number shortcuts 1..9
	if n := msg.String(); len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
		idx := int(n[0] - '1')
		if idx < len(launcherChoices) {
			m.cursor = idx
			return m.activate()
		}
	}
	return m, nil
}

func (m launcherModel) updateView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.result = LauncherQuit
		return m, tea.Quit
	case "esc", "q", "enter", "backspace", "b":
		m.state = launcherStateMenu
		return m, nil
	}
	// Forward navigation keys to the viewport for scrolling.
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// activate runs the action selected by m.cursor.
func (m launcherModel) activate() (tea.Model, tea.Cmd) {
	choice := launcherChoices[m.cursor]
	switch choice.action {
	case actionStartConversion:
		m.result = LauncherStartConversion
		return m, tea.Quit
	case actionQuit:
		m.result = LauncherQuit
		return m, tea.Quit
	case actionRecent:
		// Hand off to the runs browser via RunLauncher's outer loop.
		m.result = launcherOpenRunsBrowser
		return m, tea.Quit
	default:
		m.viewTitle = choice.label
		m.vp.SetContent(m.runAction(choice.action))
		m.vp.GotoTop()
		m.state = launcherStateView
		return m, nil
	}
}

// runAction executes a read-only action and returns its captured output.
func (m launcherModel) runAction(a launcherAction) string {
	if m.store == nil {
		msg := "Database unavailable.\n\nThe database file could not be opened:\n  " + defaultDBPath()
		if m.storeErr != "" {
			msg += "\n\nReason:\n  " + m.storeErr
		} else {
			msg += "\n\nNo conversions have been recorded yet."
		}
		return msg
	}
	var buf bytes.Buffer
	ctx := context.Background()
	var err error
	switch a {
	case actionStats:
		err = RenderStats(ctx, &buf, m.store, "")
	case actionRecent:
		err = RenderRecent(ctx, &buf, m.store, 20)
	case actionErrors:
		err = RenderErrors(ctx, &buf, m.store, "", "")
	case actionNotBeneficial:
		err = RenderNotBeneficial(ctx, &buf, m.store, "")
	case actionFormats:
		err = RenderFormats(ctx, &buf, m.store, "")
	case actionSpaceSaved:
		err = RenderSpaceSaved(ctx, &buf, m.store, "total")
	case actionDashboard:
		outPath, derr := dashboard.Generate(ctx, m.store, "")
		if derr != nil {
			err = derr
			break
		}
		fmt.Fprintf(&buf, "Dashboard generated: %s\n\n", outPath)
		if oerr := openURL(outPath); oerr != nil {
			fmt.Fprintf(&buf, "Could not open browser: %v\n", oerr)
		} else {
			fmt.Fprintln(&buf, "Opened in your default browser.")
		}
	}
	if err != nil {
		fmt.Fprintf(&buf, "Error: %v\n", err)
	}
	return buf.String()
}

// ── View ─────────────────────────────────────────────────────────────────────

var (
	launcherStyleBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#4B5563"))

	launcherStyleTitle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7C3AED")).
				Bold(true)

	launcherStyleHint = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6B7280"))

	launcherStyleItem = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E5E7EB"))

	launcherStyleItemSel = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7C3AED")).
				Bold(true)

	launcherStyleItemHint = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6B7280"))

	launcherStyleHeader = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A78BFA")).
				Bold(true)
)

func (m launcherModel) View() string {
	w := m.width - 2
	if w < 50 {
		w = 50
	}

	var b strings.Builder
	b.WriteString(launcherStyleTitle.Render("Reforge") +
		launcherStyleHint.Render(" v"+AppVersion) +
		launcherStyleTitle.Render("  —  Main Menu") + "\n\n")

	switch m.state {
	case launcherStateMenu:
		b.WriteString(m.viewMenu())
	case launcherStateView:
		b.WriteString(m.viewReport())
	}

	return launcherStyleBorder.Width(w).MaxHeight(m.height).Render(b.String()) + "\n"
}

func (m launcherModel) viewMenu() string {
	var b strings.Builder
	b.WriteString(launcherStyleHint.Render("  [↑/k] [↓/j] navigate   [Enter] select   [1-9] shortcut   [q/Esc] quit") + "\n\n")
	for i, c := range launcherChoices {
		cursor := "  "
		labelStyle := launcherStyleItem
		if i == m.cursor {
			cursor = launcherStyleItemSel.Render("❯ ")
			labelStyle = launcherStyleItemSel
		}
		num := fmt.Sprintf("%d. ", i+1)
		line := cursor + launcherStyleHint.Render(num) + labelStyle.Render(c.label)
		if c.hint != "" {
			line += launcherStyleItemHint.Render("  — " + c.hint)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (m launcherModel) viewReport() string {
	var b strings.Builder
	b.WriteString(launcherStyleHeader.Render("  "+m.viewTitle) + "\n")
	b.WriteString(launcherStyleHint.Render("  [↑/↓ / PgUp/PgDn] scroll   [Esc/q/Enter] back to menu   [Ctrl+C] quit") + "\n\n")
	if m.viewReady {
		b.WriteString(m.vp.View())
	}
	return b.String()
}

// viewportSize returns the width/height available to the report viewport,
// leaving room for the border, title, and hint lines drawn around it.
func (m launcherModel) viewportSize() (int, int) {
	w := m.width - 6 // border + padding
	if w < 40 {
		w = 40
	}
	h := m.height - 8 // border + title + hint + spacing
	if h < 6 {
		h = 6
	}
	return w, h
}
