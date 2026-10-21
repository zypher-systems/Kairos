// Package logutil turns raw process output into log chunks that are safe to
// store as text.
package logutil

import "unicode/utf8"

// Splitter cuts a byte stream into bounded chunks on rune boundaries. A
// multibyte character split across two writes is carried over to the next
// call instead of being emitted as two invalid halves.
type Splitter struct {
	// Max is the largest chunk emitted, in bytes. Zero means 16 KiB.
	Max  int
	rest []byte
}

// Push appends p to the pending bytes and returns the complete chunks.
func (s *Splitter) Push(p []byte) []string {
	max := s.Max
	if max <= 0 {
		max = 16 * 1024
	}
	s.rest = append(s.rest, p...)
	var out []string
	for len(s.rest) > 0 {
		n := len(s.rest)
		if n > max {
			n = max
		}
		// Back up to a rune start so the chunk never ends mid-character.
		for n > 0 && n < len(s.rest) && !utf8.RuneStart(s.rest[n]) {
			n--
		}
		if n == len(s.rest) {
			// The tail may be an incomplete rune: keep up to 3 bytes back
			// while they could still complete.
			if keep := incompleteTail(s.rest); keep > 0 {
				n -= keep
			}
		}
		if n == 0 {
			break
		}
		out = append(out, string(s.rest[:n]))
		s.rest = append(s.rest[:0], s.rest[n:]...)
	}
	return out
}

// Flush returns whatever is pending, including an incomplete rune, which
// the store then replaces with U+FFFD.
func (s *Splitter) Flush() string {
	if len(s.rest) == 0 {
		return ""
	}
	out := string(s.rest)
	s.rest = s.rest[:0]
	return out
}

// incompleteTail reports how many trailing bytes form the start of a rune
// that has not fully arrived yet (0 when the tail is complete or invalid).
func incompleteTail(b []byte) int {
	for back := 1; back <= 3 && back <= len(b); back++ {
		c := b[len(b)-back]
		if !utf8.RuneStart(c) {
			continue
		}
		if c < 0x80 {
			return 0
		}
		want := 0
		switch {
		case c&0xE0 == 0xC0:
			want = 2
		case c&0xF0 == 0xE0:
			want = 3
		case c&0xF8 == 0xF0:
			want = 4
		default:
			return 0 // invalid lead byte; let the store replace it
		}
		if back < want {
			return back
		}
		return 0
	}
	return 0
}
