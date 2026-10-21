package httpapi

import (
	"testing"
	"time"
)

func TestLoginLimiterWindow(t *testing.T) {
	l := newLoginLimiter(3, time.Minute)
	base := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	now := base
	l.now = func() time.Time { return now }

	if !l.allow("1.1.1.1") || !l.allow("1.1.1.1") || !l.allow("1.1.1.1") {
		t.Fatal("first three attempts must pass")
	}
	if l.allow("1.1.1.1") {
		t.Fatal("fourth attempt in the window must fail")
	}
	if !l.allow("2.2.2.2") {
		t.Fatal("a different address has its own budget")
	}

	now = base.Add(time.Minute + time.Second)
	if !l.allow("1.1.1.1") {
		t.Fatal("after the window the address must be allowed again")
	}
}
