package ffmpeg

import (
	"strings"
	"testing"
)

func TestBoundedBuf_SmallInputUnchanged(t *testing.T) {
	var b boundedBuf
	b.WriteString("hello\n")
	b.WriteString("world\n")
	if got := b.String(); got != "hello\nworld\n" {
		t.Errorf("got %q", got)
	}
}

func TestBoundedBuf_KeepsHeadAndTailAndBoundsMemory(t *testing.T) {
	var b boundedBuf
	b.WriteString("FIRST-ERROR\n")
	line := strings.Repeat("x", 99) + "\n"
	for i := 0; i < 20000; i++ { // ~2 MB
		b.WriteString(line)
	}
	b.WriteString("LAST-ERROR\n")

	got := b.String()
	if !strings.HasPrefix(got, "FIRST-ERROR") {
		t.Error("head was lost")
	}
	if !strings.HasSuffix(got, "LAST-ERROR\n") {
		t.Error("tail was lost")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("missing truncation marker")
	}
	if max := stderrHeadLimit + stderrTailLimit + len(stderrMarker); len(got) > max {
		t.Errorf("len = %d, want <= %d", len(got), max)
	}
	if cap(b.tail) > 8*stderrTailLimit {
		t.Errorf("tail capacity %d not bounded", cap(b.tail))
	}
}
