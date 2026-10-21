package logutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitterCarriesPartialRune(t *testing.T) {
	s := &Splitter{Max: 1024}
	// "é" is C3 A9; deliver the first byte alone.
	got := s.Push([]byte("caf\xc3"))
	if len(got) != 1 || got[0] != "caf" {
		t.Fatalf("first push: %q", got)
	}
	got = s.Push([]byte("\xa9 ok"))
	if len(got) != 1 || got[0] != "é ok" {
		t.Fatalf("second push: %q", got)
	}
	if s.Flush() != "" {
		t.Fatal("nothing should be pending")
	}
}

func TestSplitterBoundsOnRuneBoundary(t *testing.T) {
	s := &Splitter{Max: 5}
	in := strings.Repeat("日本", 4) // 3 bytes each
	got := s.Push([]byte(in))
	got = append(got, s.Flush())
	var joined string
	for _, c := range got {
		if len(c) > 5 {
			t.Fatalf("chunk over max: %d", len(c))
		}
		if !utf8.ValidString(c) {
			t.Fatalf("invalid chunk %q", c)
		}
		joined += c
	}
	if joined != in {
		t.Fatalf("lost bytes: %q", joined)
	}
}

func TestSplitterFlushesInvalidTail(t *testing.T) {
	s := &Splitter{Max: 1024}
	if got := s.Push([]byte("x\xff")); len(got) != 1 || got[0] != "x\xff" {
		t.Fatalf("invalid byte must not be held back: %q", got)
	}
	s.Push([]byte("\xe6\x97"))
	if got := s.Flush(); got != "\xe6\x97" {
		t.Fatalf("flush must release the partial rune: %q", got)
	}
}
