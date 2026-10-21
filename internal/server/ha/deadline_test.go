package ha

import (
	"testing"
	"time"
)

// A leader whose renewals stop (database stall) must stop acting as leader
// on its own clock, before its lease can expire for the other replicas.
func TestLeaderStepsDownWhenRenewalsStop(t *testing.T) {
	l := New(nil, "a", 15*time.Second, 5*time.Second)
	l.mu.Lock()
	l.held = true
	l.deadline = time.Now().Add(time.Hour)
	l.mu.Unlock()
	if !l.IsLeader() {
		t.Fatal("fresh lease should lead")
	}
	l.mu.Lock()
	l.deadline = time.Now().Add(-time.Millisecond)
	l.mu.Unlock()
	if l.IsLeader() {
		t.Fatal("expired local deadline must not lead, even though held is still true")
	}
	if got := l.margin(); got != 3*time.Second {
		t.Fatalf("margin %s", got)
	}
}
