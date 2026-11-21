package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// testModel returns a minimal model suitable for unit tests (no setup, no answers channel).
func testModel() model {
	ch := make(chan SetupAnswers, 1)
	m := initialModel(SetupOptions{}, ch)
	// Drain the channel so goroutines don't block.
	go func() { <-ch }()
	return m
}

// ── renderBar ────────────────────────────────────────────────────────────────

func TestRenderBarWidth(t *testing.T) {
	for _, tc := range []struct{ pct, width int }{
		{0, 20},
		{50, 20},
		{100, 20},
		{0, 1},
		{100, 1},
		{75, 40},
	} {
		result := renderBar(tc.pct, tc.width)
		got := lipgloss.Width(result)
		if got != tc.width {
			t.Errorf("renderBar(%d, %d): lipgloss.Width=%d, want %d", tc.pct, tc.width, got, tc.width)
		}
	}
}

func TestRenderBarZeroWidth(t *testing.T) {
	if renderBar(50, 0) != "" {
		t.Error("renderBar with width=0 should return empty string")
	}
	if renderBar(50, -1) != "" {
		t.Error("renderBar with width=-1 should return empty string")
	}
}

func TestRenderBarClampsPct(t *testing.T) {
	// pct > 100 should not panic and should fill the bar
	result := renderBar(150, 10)
	got := lipgloss.Width(result)
	if got != 10 {
		t.Errorf("renderBar(150, 10): width=%d, want 10", got)
	}
}

// ── padBetween ───────────────────────────────────────────────────────────────

func TestPadBetweenBasic(t *testing.T) {
	result := padBetween("LEFT", "RIGHT", 30)
	if !strings.Contains(result, "LEFT") {
		t.Error("padBetween result missing 'LEFT'")
	}
	if !strings.Contains(result, "RIGHT") {
		t.Error("padBetween result missing 'RIGHT'")
	}
	// Should be at least width wide
	w := lipgloss.Width(result)
	if w < 30 {
		t.Errorf("padBetween width=%d, want >= 30", w)
	}
}

func TestPadBetweenMinGap(t *testing.T) {
	// When left+right > width, gap is forced to 1
	result := padBetween("VERYLONGLEFT", "VERYLONGRIGHT", 5)
	if !strings.Contains(result, "VERYLONGLEFT") {
		t.Error("padBetween missing left with narrow width")
	}
}

func TestPadBetweenEmptyRight(t *testing.T) {
	result := padBetween("LEFT", "", 20)
	if !strings.Contains(result, "LEFT") {
		t.Error("padBetween with empty right missing 'LEFT'")
	}
}

// ── fmtDuration ──────────────────────────────────────────────────────────────

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{45 * time.Second, "0:45"},
		{90 * time.Second, "1:30"},
		{60 * time.Second, "1:00"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"},
		{2*time.Hour + 30*time.Minute + 0*time.Second, "2:30:00"},
		{59*time.Second + 500*time.Millisecond, "1:00"}, // rounds to 1 minute
	}
	for _, tc := range tests {
		got := fmtDuration(tc.d)
		if got != tc.want {
			t.Errorf("fmtDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// ── truncate ─────────────────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"hello", 0, ""},
		{"hello", 3, "hel"}, // maxLen<=3: no ellipsis
		{"hello", 2, "he"},
		{"hello", 1, "h"},
		{"", 5, ""},
		// Wide runes are 2 cells: with 4 cells only "..." fits (a 2-cell rune
		// plus "..." would be 5 cells and overflow the line).
		{"日本語テスト", 4, "..."},
		{"日本語テスト", 5, "日..."}, // 2 cells + "..." = 5
	}
	for _, tc := range tests {
		got := truncate(tc.s, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.s, tc.maxLen, got, tc.want)
		}
	}
}

// ── truncateANSI ─────────────────────────────────────────────────────────────

func TestTruncateANSI(t *testing.T) {
	// Plain string under limit — returned as-is
	s := "hello"
	if got := truncateANSI(s, 10); got != s {
		t.Errorf("truncateANSI plain under limit: got %q, want %q", got, s)
	}

	// Zero limit
	if got := truncateANSI("hello", 0); got != "" {
		t.Errorf("truncateANSI zero limit: got %q, want empty", got)
	}

	// Plain string over limit
	long := strings.Repeat("x", 30)
	got := truncateANSI(long, 10)
	if lipgloss.Width(got) > 10 {
		t.Errorf("truncateANSI over limit: width=%d, want <=10", lipgloss.Width(got))
	}
}

// ── model.logHeight ──────────────────────────────────────────────────────────

func TestLogHeightNoJobs(t *testing.T) {
	m := testModel()
	m.height = 24
	// used = 3 + 0*3 = 3; h = 24 - 4 - 3 = 17
	got := m.logHeight()
	if got != 17 {
		t.Errorf("logHeight() with 0 jobs = %d, want 17", got)
	}
}

func TestLogHeightOneJob(t *testing.T) {
	m := testModel()
	m.height = 24
	m.jobs = []*jobState{{}}
	// used = 3 + 1*3 + 1(blank) = 7; h = 24 - 4 - 7 = 13
	got := m.logHeight()
	if got != 13 {
		t.Errorf("logHeight() with 1 job = %d, want 13", got)
	}
}

func TestLogHeightMinimum(t *testing.T) {
	m := testModel()
	m.height = 5 // very short terminal
	got := m.logHeight()
	if got < 3 {
		t.Errorf("logHeight() minimum should be 3, got %d", got)
	}
}

// ── model.lastLogLines ────────────────────────────────────────────────────────

func TestLastLogLinesEmpty(t *testing.T) {
	m := testModel()
	got := m.lastLogLines(5)
	if len(got) != 0 {
		t.Errorf("lastLogLines on empty: len=%d, want 0", len(got))
	}
}

func TestLastLogLinesFewerThanN(t *testing.T) {
	m := testModel()
	m.logLines = []logLine{{text: "a"}, {text: "b"}}
	got := m.lastLogLines(5)
	if len(got) != 2 {
		t.Errorf("lastLogLines fewer than n: len=%d, want 2", len(got))
	}
}

func TestLastLogLinesMoreThanN(t *testing.T) {
	m := testModel()
	for i := 0; i < 10; i++ {
		m.logLines = append(m.logLines, logLine{text: "line"})
	}
	got := m.lastLogLines(3)
	if len(got) != 3 {
		t.Errorf("lastLogLines more than n: len=%d, want 3", len(got))
	}
}

// ── model.renderLogLine ───────────────────────────────────────────────────────

func TestRenderLogLineKinds(t *testing.T) {
	m := testModel()
	m.width = 100
	kinds := []completionKind{kindKept, kindDiscard, kindError, kindSkipped}
	for _, k := range kinds {
		ll := logLine{text: "test line", kind: k}
		got := m.renderLogLine(ll, 80)
		if got == "" {
			t.Errorf("renderLogLine kind=%d returned empty string", k)
		}
		// Should contain the text (possibly with ANSI codes around it)
		if !strings.Contains(got, "test line") {
			t.Errorf("renderLogLine kind=%d doesn't contain text: %q", k, got)
		}
	}
}

// ── model.renderSectionSep ────────────────────────────────────────────────────

func TestRenderSectionSep(t *testing.T) {
	m := testModel()
	result := m.renderSectionSep("Log", 80)
	if !strings.Contains(result, "Log") {
		t.Errorf("renderSectionSep missing label: %q", result)
	}
}

func TestRenderSectionSepNarrow(t *testing.T) {
	m := testModel()
	// Should not panic with very narrow width
	result := m.renderSectionSep("Log", 3)
	if !strings.Contains(result, "Log") {
		t.Errorf("renderSectionSep narrow missing label: %q", result)
	}
}

// ── model.renderControlBar ────────────────────────────────────────────────────

func TestRenderControlBarDefault(t *testing.T) {
	m := testModel()
	m.width = 100
	result := m.renderControlBar(80)
	if !strings.Contains(result, "[p]") {
		t.Errorf("default control bar should contain [p]: %q", result)
	}
	if !strings.Contains(result, "[q]") {
		t.Errorf("default control bar should contain [q]: %q", result)
	}
}

func TestRenderControlBarPaused(t *testing.T) {
	m := testModel()
	m.paused = true
	result := m.renderControlBar(80)
	if !strings.Contains(result, "Paused") {
		t.Errorf("paused control bar should contain 'Paused': %q", result)
	}
}

func TestRenderControlBarStopKindNow(t *testing.T) {
	m := testModel()
	m.stopKind = StopKindNow
	result := m.renderControlBar(80)
	if !strings.Contains(result, "Cancelling") {
		t.Errorf("stopKindNow bar should contain 'Cancelling': %q", result)
	}
}

func TestRenderControlBarStopKindAfterCurrent(t *testing.T) {
	m := testModel()
	m.stopKind = StopKindAfterCurrent
	result := m.renderControlBar(80)
	if !strings.Contains(result, "Finishing") {
		t.Errorf("stopKindAfterCurrent bar should contain 'Finishing': %q", result)
	}
}

func TestRenderControlBarUserScrolled(t *testing.T) {
	m := testModel()
	m.userScrolled = true
	result := m.renderControlBar(80)
	if !strings.Contains(result, "[G]") {
		t.Errorf("userScrolled bar should contain '[G]': %q", result)
	}
}

// ── model.Update — message handling ──────────────────────────────────────────

func TestUpdateMsgJobStart(t *testing.T) {
	m := testModel()
	m.width = 100
	m.height = 40
	msg := msgJobStart{
		id:           42,
		label:        "myvideo.mp4",
		fileNum:      1,
		totalFiles:   5,
		heightP:      1080,
		totalSeconds: 120.0,
	}
	updated, _ := m.Update(msg)
	m2 := updated.(model)
	if len(m2.jobs) != 1 {
		t.Fatalf("after msgJobStart: len(jobs)=%d, want 1", len(m2.jobs))
	}
	if m2.jobs[0].id != 42 {
		t.Errorf("job id=%d, want 42", m2.jobs[0].id)
	}
	if m2.jobs[0].label != "myvideo.mp4" {
		t.Errorf("job label=%q, want 'myvideo.mp4'", m2.jobs[0].label)
	}
	if _, ok := m2.jobIndex[42]; !ok {
		t.Error("job not in jobIndex")
	}
}

func TestUpdateMsgJobProgress(t *testing.T) {
	m := testModel()
	js := &jobState{id: 1, label: "test.mp4"}
	m.jobs = []*jobState{js}
	m.jobIndex = map[uint64]*jobState{1: js}

	updated, _ := m.Update(msgJobProgress{id: 1, pct: 55})
	m2 := updated.(model)
	if m2.jobs[0].pct != 55 {
		t.Errorf("pct=%d, want 55", m2.jobs[0].pct)
	}
}

func TestUpdateMsgJobProgressUnknownID(t *testing.T) {
	m := testModel()
	// Should not panic for unknown id
	_, _ = m.Update(msgJobProgress{id: 999, pct: 50})
}

func TestUpdateMsgJobComplete(t *testing.T) {
	m := testModel()
	js := &jobState{id: 1, label: "test.mp4"}
	m.jobs = []*jobState{js}
	m.jobIndex = map[uint64]*jobState{1: js}

	updated, _ := m.Update(msgJobComplete{id: 1, summary: "✓ done", kind: kindKept})
	m2 := updated.(model)

	if len(m2.jobs) != 0 {
		t.Errorf("after complete: len(jobs)=%d, want 0", len(m2.jobs))
	}
	if _, ok := m2.jobIndex[1]; ok {
		t.Error("job still in jobIndex after complete")
	}
	if len(m2.logLines) != 1 {
		t.Fatalf("logLines len=%d, want 1", len(m2.logLines))
	}
	if m2.logLines[0].text != "✓ done" {
		t.Errorf("logLine text=%q, want '✓ done'", m2.logLines[0].text)
	}
	if m2.logLines[0].kind != kindKept {
		t.Errorf("logLine kind=%d, want kindKept", m2.logLines[0].kind)
	}
}

func TestUpdateMsgLog(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(msgLog{text: "info message"})
	m2 := updated.(model)
	if len(m2.logLines) != 1 {
		t.Fatalf("logLines len=%d, want 1", len(m2.logLines))
	}
	if m2.logLines[0].text != "info message" {
		t.Errorf("logLine text=%q, want 'info message'", m2.logLines[0].text)
	}
	if m2.logLines[0].kind != kindSkipped {
		t.Errorf("msgLog should use kindSkipped, got %d", m2.logLines[0].kind)
	}
}

func TestUpdateMsgLogTrimming(t *testing.T) {
	m := testModel()
	// Add 201 log lines — should trim to maxLogLines (200)
	for i := 0; i < 201; i++ {
		m.logLines = append(m.logLines, logLine{text: "line"})
	}
	updated, _ := m.Update(msgLog{text: "new"})
	m2 := updated.(model)
	if len(m2.logLines) > maxLogLines {
		t.Errorf("logLines not trimmed: len=%d, want <=%d", len(m2.logLines), maxLogLines)
	}
}

func TestUpdateMsgControlState(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(msgControlState{paused: true, stopping: StopKindAfterCurrent})
	m2 := updated.(model)
	if !m2.paused {
		t.Error("paused should be true after msgControlState")
	}
	if m2.stopKind != StopKindAfterCurrent {
		t.Errorf("stopKind=%d, want StopKindAfterCurrent", m2.stopKind)
	}
}

func TestUpdateMsgStartTimer(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(msgStartTimer{})
	m2 := updated.(model)
	if !m2.timerActive {
		t.Error("timerActive should be true after msgStartTimer")
	}
	if m2.timerStart.IsZero() {
		t.Error("timerStart should be set after msgStartTimer")
	}
}

// ── key handling ─────────────────────────────────────────────────────────────

func TestKeyPauseReturnsCmd(t *testing.T) {
	m := testModel()
	called := false
	m.onControl = func(a controlAction) { called = true }

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd == nil {
		t.Error("key 'p' should return a non-nil cmd")
	}
	// Execute the cmd — it should call onControl asynchronously
	if cmd != nil {
		cmd() // returns nil tea.Msg, but fires onControl
	}
	if !called {
		t.Error("onControl was not called after executing cmd from 'p'")
	}
}

func TestKeyStopAfterCurrentReturnsCmd(t *testing.T) {
	m := testModel()
	called := false
	m.onControl = func(a controlAction) { called = true }

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Error("key 's' should return non-nil cmd")
	}
	if cmd != nil {
		cmd()
	}
	if !called {
		t.Error("onControl not called for 's'")
	}
}

func TestKeyQuitRequiresConfirmation(t *testing.T) {
	m := testModel()
	calls := 0
	m.onControl = func(a controlAction) {
		if a == actionStopNow {
			calls++
		}
	}

	// First 'q' only asks for confirmation.
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Error("'q' must not stop immediately; it should ask for confirmation")
	}
	m = mm.(model)
	if !m.confirmQuit {
		t.Fatal("confirmQuit not set after 'q'")
	}

	// Confirming with 'y' fires the stop.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("'y' should confirm and return a cmd")
	}
	cmd()
	if calls != 1 {
		t.Errorf("StopNow fired %d times, want 1", calls)
	}
}

func TestKeyStopAfterCurrentNoOpIfAlreadyStopping(t *testing.T) {
	m := testModel()
	m.stopKind = StopKindAfterCurrent // already stopping
	called := false
	m.onControl = func(a controlAction) { called = true }

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	// Should NOT fire action since already stopping
	if cmd != nil {
		cmd()
	}
	if called {
		t.Error("onControl should NOT be called for 's' when already stopping")
	}
}

func TestKeyQuitNoOpIfAlreadyCancelling(t *testing.T) {
	m := testModel()
	m.stopKind = StopKindNow
	called := false
	m.onControl = func(a controlAction) { called = true }

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		cmd()
	}
	if called {
		t.Error("onControl should NOT be called for 'q' when stopKindNow")
	}
}

func TestKeyGClearsUserScrolled(t *testing.T) {
	m := testModel()
	m.userScrolled = true
	m.vpReady = true
	m.width = 100
	m.height = 40
	m.vp = viewport.New(80, 20)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m2 := updated.(model)
	if m2.userScrolled {
		t.Error("key 'G' should clear userScrolled when vpReady=true")
	}
}

func TestKeyGNoOpWhenVpNotReady(t *testing.T) {
	m := testModel()
	m.userScrolled = true
	m.vpReady = false
	// G does nothing when vpReady=false — userScrolled unchanged
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m2 := updated.(model)
	if !m2.userScrolled {
		t.Error("key 'G' should not clear userScrolled when vpReady=false")
	}
}

func TestKeyScrollWhenVpNotReady(t *testing.T) {
	m := testModel()
	m.vpReady = false
	// Should not panic
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
}

func TestKeyWithNilOnControl(t *testing.T) {
	m := testModel()
	m.onControl = nil
	// Should not panic
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
}
