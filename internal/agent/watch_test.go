package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/protocol"
)

// fakeServer answers watch events with the queued statuses, then 202.
type fakeServer struct {
	mu       sync.Mutex
	statuses []int
	events   []protocol.WatchEvent
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var ev protocol.WatchEvent
	_ = json.NewDecoder(r.Body).Decode(&ev)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	status := http.StatusAccepted
	if len(f.statuses) > 0 {
		status, f.statuses = f.statuses[0], f.statuses[1:]
	}
	w.WriteHeader(status)
}

func (f *fakeServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// settle drops a file and polls until the change is reported.
func settle(w *Watcher, fw *fileWatch, dir, name string) error {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
		return err
	}
	w.poll(context.Background(), fw) // first sighting
	w.poll(context.Background(), fw) // stable: report
	return nil
}

// A report the server did not accept stays pending: the next poll sends it
// again without the files having to change.
func TestWatchEventIsRetriedUntilAccepted(t *testing.T) {
	srv := &fakeServer{statuses: []int{http.StatusBadGateway, http.StatusServiceUnavailable}}
	hs := httptest.NewServer(srv)
	defer hs.Close()

	dir := t.TempDir()
	w := NewWatcher(NewClient(hs.URL, "token", "test"))
	fw := &fileWatch{jobID: uuid.New(), glob: "*.csv", root: dir, interval: time.Second}

	w.poll(context.Background(), fw) // baseline
	if err := settle(w, fw, dir, "a.csv"); err != nil {
		t.Fatal(err)
	}
	if got := srv.count(); got != 1 {
		t.Fatalf("settled change must be reported once, got %d", got)
	}
	w.poll(context.Background(), fw)
	if got := srv.count(); got != 2 {
		t.Fatalf("a refused report must be sent again, got %d", got)
	}
	w.poll(context.Background(), fw)
	if got := srv.count(); got != 3 {
		t.Fatalf("still refused, must be sent again, got %d", got)
	}
	w.poll(context.Background(), fw)
	w.poll(context.Background(), fw)
	if got := srv.count(); got != 3 {
		t.Fatalf("an accepted report must not repeat, got %d", got)
	}
	if ev := srv.events[2]; len(ev.Files) != 1 || ev.Files[0] != "a.csv" {
		t.Fatalf("retried event carries %v", ev.Files)
	}
}

// A change that lands while a report is pending replaces it: the settle
// window applies to the new set and the next report carries both files.
func TestWatchRetryFollowsNewFiles(t *testing.T) {
	srv := &fakeServer{statuses: []int{http.StatusBadGateway}}
	hs := httptest.NewServer(srv)
	defer hs.Close()

	dir := t.TempDir()
	w := NewWatcher(NewClient(hs.URL, "token", "test"))
	fw := &fileWatch{jobID: uuid.New(), glob: "*.csv", root: dir, interval: time.Second}

	w.poll(context.Background(), fw)
	if err := settle(w, fw, dir, "a.csv"); err != nil {
		t.Fatal(err)
	}
	if err := settle(w, fw, dir, "b.csv"); err != nil {
		t.Fatal(err)
	}
	if got := srv.count(); got != 2 {
		t.Fatalf("got %d reports", got)
	}
	if ev := srv.events[1]; len(ev.Files) != 2 {
		t.Fatalf("report must carry both files, got %v", ev.Files)
	}
}

// Some 4xx answers pass: a proxy timing out, a rate limit, or a token the
// server could not check. The event stays pending through those.
func TestWatchRetriesPassing4xx(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusUnauthorized,
	} {
		srv := &fakeServer{statuses: []int{status}}
		hs := httptest.NewServer(srv)
		dir := t.TempDir()
		w := NewWatcher(NewClient(hs.URL, "token", "test"))
		fw := &fileWatch{jobID: uuid.New(), glob: "*.csv", root: dir, interval: time.Second}
		w.poll(context.Background(), fw)
		if err := settle(w, fw, dir, "a.csv"); err != nil {
			t.Fatal(err)
		}
		w.poll(context.Background(), fw)
		w.poll(context.Background(), fw)
		hs.Close()
		if got := srv.count(); got != 2 {
			t.Fatalf("status %d: sent %d times, want the refusal and one accepted retry", status, got)
		}
	}
}

// A definite refusal will not change on a retry: the job is gone or the
// watch moved. The report is dropped instead of repeating on every poll.
func TestWatchDropsADefiniteRefusal(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusBadRequest, http.StatusGone} {
		srv := &fakeServer{statuses: []int{status}}
		hs := httptest.NewServer(srv)
		dir := t.TempDir()
		w := NewWatcher(NewClient(hs.URL, "token", "test"))
		fw := &fileWatch{jobID: uuid.New(), glob: "*.csv", root: dir, interval: time.Second}
		w.poll(context.Background(), fw)
		if err := settle(w, fw, dir, "a.csv"); err != nil {
			t.Fatal(err)
		}
		w.poll(context.Background(), fw)
		w.poll(context.Background(), fw)
		hs.Close()
		if got := srv.count(); got != 1 {
			t.Fatalf("status %d: reported %d times", status, got)
		}
	}
}
