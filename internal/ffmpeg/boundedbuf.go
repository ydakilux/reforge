package ffmpeg

import "bytes"

const (
	stderrHeadLimit = 64 * 1024  // keep the first 64 KB (encoder init errors live here)
	stderrTailLimit = 192 * 1024 // and the last 192 KB (the fatal error lives here)
	stderrMarker    = "\n[... stderr truncated ...]\n"
)

// boundedBuf collects text but caps memory use: it keeps the head and the most
// recent tail of what was written, dropping the middle. A multi-hour encode
// that emits a warning per frame would otherwise grow stderr without limit.
type boundedBuf struct {
	head      bytes.Buffer
	tail      []byte
	truncated bool
}

func (b *boundedBuf) WriteString(s string) {
	if b.head.Len() < stderrHeadLimit {
		room := stderrHeadLimit - b.head.Len()
		if len(s) <= room {
			b.head.WriteString(s)
			return
		}
		b.head.WriteString(s[:room])
		s = s[room:]
	}
	b.tail = append(b.tail, s...)
	if len(b.tail) > 2*stderrTailLimit {
		b.tail = append([]byte(nil), b.tail[len(b.tail)-stderrTailLimit:]...)
		b.truncated = true
	}
}

func (b *boundedBuf) WriteByte(c byte) error {
	b.WriteString(string(c))
	return nil
}

func (b *boundedBuf) String() string {
	tail := b.tail
	if len(tail) > stderrTailLimit {
		tail = tail[len(tail)-stderrTailLimit:]
		b.truncated = true
	}
	if !b.truncated {
		return b.head.String() + string(tail)
	}
	return b.head.String() + stderrMarker + string(tail)
}
