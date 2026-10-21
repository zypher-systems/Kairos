package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/protocol"
	"github.com/zypher-systems/kairos/internal/storage"
)

// fileWatch polls one glob on the agent machine and reports arrivals to the
// server. The server owns baseline + firing semantics; the agent reports a
// signature of the current matched set and the server decides.
type fileWatch struct {
	jobID    uuid.UUID
	glob     string
	root     string
	interval time.Duration
	lastSent string // last signature we reported (or baselined)
	settle   string // candidate signature seen once, awaiting confirmation
	armed    bool   // baseline taken — first sighting reports nothing
}

// Watcher runs all file-watch assignments pushed by the server.
type Watcher struct {
	client *Client
	mu     sync.Mutex
	tasks  map[uuid.UUID]*fileWatch
	cancel map[uuid.UUID]context.CancelFunc
}

// NewWatcher builds the agent-side watch supervisor.
func NewWatcher(client *Client) *Watcher {
	return &Watcher{
		client: client,
		tasks:  map[uuid.UUID]*fileWatch{},
		cancel: map[uuid.UUID]context.CancelFunc{},
	}
}

// Sync applies the server-pushed watch set: starts new, removes revoked,
// restarts changed.
func (w *Watcher) Sync(ctx context.Context, tasks []protocol.WatchTask) {
	want := map[uuid.UUID]protocol.WatchTask{}
	for _, t := range tasks {
		want[t.JobID] = t
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, c := range w.cancel {
		cur := w.tasks[id]
		next, ok := want[id]
		if !ok || cur.glob != next.Glob || cur.root != next.Root || cur.interval != time.Duration(next.Interval)*time.Second {
			c()
			delete(w.cancel, id)
			delete(w.tasks, id)
			slog.Info("watch stopped", "job", id)
		}
	}
	for id, t := range want {
		if _, ok := w.cancel[id]; ok {
			continue
		}
		fw := &fileWatch{
			jobID:    id,
			glob:     t.Glob,
			root:     t.Root,
			interval: time.Duration(t.Interval) * time.Second,
		}
		wctx, cancel := context.WithCancel(ctx)
		w.tasks[id] = fw
		w.cancel[id] = cancel
		go w.loop(wctx, fw)
		slog.Info("watch started", "job", id, "glob", t.Glob, "interval", fw.interval)
	}
}

func (w *Watcher) loop(ctx context.Context, fw *fileWatch) {
	t := time.NewTicker(fw.interval)
	defer t.Stop()
	// First poll immediately.
	w.poll(ctx, fw)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.poll(ctx, fw)
		}
	}
}

// poll computes the current signature and reports it once it has been
// stable across two consecutive polls (settle window).
func (w *Watcher) poll(ctx context.Context, fw *fileWatch) {
	sig, files, err := globSignature(fw.root, fw.glob)
	if err != nil {
		slog.Debug("watch poll", "job", fw.jobID, "glob", fw.glob, "err", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !fw.armed {
		// Baseline: pre-existing files never fire.
		fw.lastSent = sig
		fw.armed = true
		return
	}
	if sig == fw.lastSent {
		fw.settle = ""
		return
	}
	if fw.settle != sig {
		fw.settle = sig
		return
	}
	// Stable change across two polls: report once.
	fw.settle = ""
	fw.lastSent = sig
	if err := w.client.ReportWatch(ctx, fw.jobID, sig, files); err != nil {
		slog.Warn("watch report", "job", fw.jobID, "err", err)
		return
	}
	slog.Info("watch fired", "job", fw.jobID, "glob", fw.glob)
}

// globSignature hashes the sorted matched-file set (name+size+mtime) and
// returns the matched paths. With a root the glob is relative to it and the
// paths come back relative too, as the server's transfer expects.
func globSignature(root, pattern string) (string, []string, error) {
	b, err := storage.NewLocal(root)
	if err != nil {
		return "", nil, err
	}
	entries, err := b.List(context.Background(), pattern)
	if err != nil {
		return "", nil, err
	}
	var lines, files []string
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("%s|%d|%d", e.Path, e.Size, e.ModTime.UnixNano()))
		files = append(files, e.Path)
	}
	sort.Strings(lines)
	sort.Strings(files)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), files, nil
}
