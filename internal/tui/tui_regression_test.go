package tui

import (
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

func runesKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// The repaint loop must survive the setup phase, otherwise the converting phase
// never repaints on its own (frozen timers/ETA, nothing while paused).
func TestTickLoopSurvivesSetupPhase(t *testing.T) {
	m := initialModel(SetupOptions{NeedBypass: true}, make(chan SetupAnswers, 1))
	if m.phase != phaseSetup {
		t.Fatal("expected setup phase")
	}
	if _, cmd := m.Update(msgTick(time.Now())); cmd == nil {
		t.Fatal("tick was not re-armed during setup phase")
	}
}

func TestTickLoopContinuesAfterSetupCompletes(t *testing.T) {
	m := initialModel(SetupOptions{NeedBypass: true}, make(chan SetupAnswers, 1))
	mm, _ := m.Update(runesKey("n")) // answer the only question
	m2 := mm.(model)
	if m2.phase != phaseConverting {
		t.Fatalf("phase = %v, want converting", m2.phase)
	}
	if _, cmd := m2.Update(msgTick(time.Now())); cmd == nil {
		t.Fatal("tick loop broken after setup")
	}
}

// Without a TTY the UI must hand back usable (non-cancelled) answers.
func TestNonTTYSetupAnswersNotCancelled(t *testing.T) {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		t.Skip("stdout is a terminal; non-TTY path not exercised")
	}
	_, ch := New(SetupOptions{}, nil)
	select {
	case a := <-ch:
		if a.Cancelled {
			t.Fatal("non-TTY mode reported setup as Cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no answers delivered")
	}
}

func TestClampPct(t *testing.T) {
	for in, want := range map[int]int{-5: 0, 0: 0, 57: 57, 100: 100, 103: 100, 9999: 100} {
		if got := clampPct(in); got != want {
			t.Errorf("clampPct(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestJobProgressIsClamped(t *testing.T) {
	m := initialModel(SetupOptions{}, make(chan SetupAnswers, 1))
	mm, _ := m.Update(msgJobStart{id: 1, label: "a.mkv", fileNum: 1, totalFiles: 1})
	mm, _ = mm.(model).Update(msgJobProgress{id: 1, pct: 103})
	js := mm.(model).jobIndex[1]
	if js == nil || js.pct != 100 {
		t.Fatalf("pct = %+v, want 100", js)
	}
	if eta := m.jobETA(js); eta[0] == '~' && len(eta) > 1 && eta[1] == '-' {
		t.Errorf("negative ETA: %q", eta)
	}
}

func TestQuitConfirmationDismissedByOtherKey(t *testing.T) {
	m := testModel()
	fired := false
	m.onControl = func(a controlAction) { fired = true }
	mm, _ := m.Update(runesKey("q"))
	mm, cmd := mm.(model).Update(runesKey("n")) // any other key cancels the prompt
	if cmd != nil {
		cmd()
	}
	if fired || mm.(model).confirmQuit {
		t.Errorf("prompt should be dismissed without stopping (fired=%v confirm=%v)", fired, mm.(model).confirmQuit)
	}
}

func TestQuitConfirmedByCtrlCTwice(t *testing.T) {
	m := testModel()
	fired := false
	m.onControl = func(a controlAction) { fired = a == actionStopNow }
	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}
	mm, _ := m.Update(ctrlC)
	_, cmd := mm.(model).Update(ctrlC)
	if cmd == nil {
		t.Fatal("second Ctrl+C should confirm")
	}
	cmd()
	if !fired {
		t.Error("StopNow not fired after double Ctrl+C")
	}
}

func TestActiveDurationExcludesPauses(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	m := model{pauses: []pauseSpan{{from: at(10), to: at(30)}, {from: at(50)}}} // second pause still open

	cases := []struct {
		from, to int
		want     time.Duration
	}{
		{0, 10, 10 * time.Second},  // before any pause
		{0, 30, 10 * time.Second},  // fully covers pause 1
		{0, 40, 20 * time.Second},  // 10 before + 10 after pause 1
		{20, 40, 10 * time.Second}, // starts inside pause 1
		{0, 60, 30 * time.Second},  // open pause counted up to "to"
		{55, 60, 0},                // entirely inside the open pause
	}
	for _, c := range cases {
		if got := m.activeDuration(at(c.from), at(c.to)); got != c.want {
			t.Errorf("activeDuration(%d,%d) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestControlStateRecordsPauseSpans(t *testing.T) {
	m := testModel()
	mm, _ := m.Update(msgControlState{paused: true})
	m = mm.(model)
	if len(m.pauses) != 1 || !m.pauses[0].to.IsZero() {
		t.Fatalf("pause not opened: %+v", m.pauses)
	}
	mm, _ = m.Update(msgControlState{paused: false})
	m = mm.(model)
	if len(m.pauses) != 1 || m.pauses[0].to.IsZero() {
		t.Fatalf("pause not closed on resume: %+v", m.pauses)
	}
}

func TestJobElapsedFreezesWhilePaused(t *testing.T) {
	m := testModel()
	start := time.Now().Add(-10 * time.Second)
	js := &jobState{id: 1, startedAt: start, pct: 50}
	// Paused for the last 6 seconds and still paused.
	m.pauses = []pauseSpan{{from: time.Now().Add(-6 * time.Second)}}
	if got := m.jobElapsed(js); got < 3*time.Second || got > 5*time.Second {
		t.Errorf("elapsed = %v, want ~4s (paused time excluded)", got)
	}
}

func TestTruncateCountsDisplayWidth(t *testing.T) {
	got := truncate("日本語日本語日本語", 9) // each rune is 2 cells wide
	if w := lipgloss.Width(got); w > 9 {
		t.Errorf("truncate produced width %d > 9: %q", w, got)
	}
	if got == "" {
		t.Error("truncate returned empty string")
	}
}
