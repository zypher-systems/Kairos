package ha

import (
	"context"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestBootPolicyFollowerStays(t *testing.T) {
	l := New(nil, "follower", time.Second, time.Second)
	if err := l.BootPolicy(); err != nil {
		t.Fatalf("follower: %v", err)
	}
	l.held = true
	l.deadline = time.Now().Add(time.Minute)
	if err := l.BootPolicy(); err != nil {
		t.Fatalf("leader: %v", err)
	}
}

func TestTwoHoldersOneLeader(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	a := New(st.Pool(), "a", 2*time.Second, 200*time.Millisecond)
	b := New(st.Pool(), "b", 2*time.Second, 200*time.Millisecond)
	ok, err := a.TryAcquire(ctx)
	if err != nil || !ok {
		t.Fatalf("a acquire: ok=%v err=%v", ok, err)
	}
	ok, err = b.TryAcquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok || b.IsLeader() {
		t.Fatal("b must not steal a live lease")
	}
	if !a.IsLeader() {
		t.Fatal("a must remain leader")
	}
}

func TestFailoverElectsNewLeader(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	a := New(st.Pool(), "a", 200*time.Millisecond, 50*time.Millisecond)
	b := New(st.Pool(), "b", 200*time.Millisecond, 50*time.Millisecond)
	ok, err := a.TryAcquire(ctx)
	if err != nil || !ok {
		t.Fatalf("a acquire: ok=%v err=%v", ok, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ok, err = b.TryAcquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ok || !b.IsLeader() {
		t.Fatal("b should become leader after a's lease expires")
	}
	ok, err = a.TryAcquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a must not steal b's live lease")
	}
}

func TestReleaseLetsOtherAcquire(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	a := New(st.Pool(), "a", 15*time.Second, 5*time.Second)
	b := New(st.Pool(), "b", 15*time.Second, 5*time.Second)
	if ok, err := a.TryAcquire(ctx); err != nil || !ok {
		t.Fatalf("a acquire: ok=%v err=%v", ok, err)
	}
	a.Release(ctx)
	ok, err := b.TryAcquire(ctx)
	if err != nil || !ok {
		t.Fatalf("b after release: ok=%v err=%v", ok, err)
	}
}
