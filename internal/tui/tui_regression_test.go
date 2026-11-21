package tui

import (
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	if eta := js.eta(); eta[0] == '~' && len(eta) > 1 && eta[1] == '-' {
		t.Errorf("negative ETA: %q", eta)
	}
}
