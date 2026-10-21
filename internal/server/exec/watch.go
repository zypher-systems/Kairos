package exec

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

	"github.com/zypher-systems/kairos/internal/server/endpoints"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
	"github.com/zypher-systems/kairos/internal/storage"
)

// Watcher polls file-trigger jobs: when the set of files matching a job's
// glob changes (and stays stable across two polls), a run is enqueued with
// trigger "file" and the matched paths recorded on it. It watches storage
// locations (mount, sftp, s3) and the legacy server-filesystem and SSH
// targets; agent targets are polled by the agent itself.
//
// State split: watch_last_sig (DB) records the last *fired* signature, so a
// process restart never re-fires for the same content. lastSeen (memory)
// implements the settle window: a signature must be identical across two
// consecutive polls before it may fire.
type Watcher struct {
	store    *store.Store
	hub      *ws.Hub
	tick     time.Duration
	mu       sync.Mutex
	nextAt   map[uuid.UUID]time.Time
	lastSeen map[uuid.UUID]string
}

// NewWatcher builds the file-trigger loop.
func NewWatcher(st *store.Store, hub *ws.Hub, tick time.Duration) *Watcher {
	if tick <= 0 {
		tick = time.Second
	}
	return &Watcher{
		store:    st,
		hub:      hub,
		tick:     tick,
		nextAt:   map[uuid.UUID]time.Time{},
		lastSeen: map[uuid.UUID]string{},
	}
}

// Run blocks until ctx is done.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.tick)
	defer t.Stop()
	slog.Info("file watcher started", "tick", w.tick.String())
	for {
		select {
		case <-ctx.Done():
			slog.Info("file watcher stopped")
			return
		case <-t.C:
			w.sweep(ctx)
		}
	}
}

func (w *Watcher) sweep(ctx context.Context) {
	jobs, err := w.store.ListWatchJobs(ctx)
	if err != nil {
		slog.Warn("list watch jobs", "err", err)
		return
	}
	now := time.Now()
	seen := map[uuid.UUID]bool{}
	for _, wj := range jobs {
		if wj.WatchTarget == "agent" {
			continue
		}
		seen[wj.ID] = true
		w.mu.Lock()
		due := now.After(w.nextAt[wj.ID])
		if due {
			w.nextAt[wj.ID] = now.Add(time.Duration(wj.WatchInterval) * time.Second)
		}
		w.mu.Unlock()
		if !due {
			continue
		}
		w.poll(ctx, wj)
	}
	// Workflows with a file trigger on a storage location.
	wfs, err := w.store.ListWatchWorkflows(ctx)
	if err != nil {
		slog.Warn("list watch workflows", "err", err)
	}
	for _, wf := range wfs {
		seen[wf.ID] = true
		w.mu.Lock()
		due := now.After(w.nextAt[wf.ID])
		if due {
			w.nextAt[wf.ID] = now.Add(time.Duration(wf.WatchIntervalSec) * time.Second)
		}
		w.mu.Unlock()
		if due {
			w.pollWorkflow(ctx, wf)
		}
	}
	// forget jobs and workflows that were deleted or disarmed
	w.mu.Lock()
	for id := range w.nextAt {
		if !seen[id] {
			delete(w.nextAt, id)
			delete(w.lastSeen, id)
		}
	}
	w.mu.Unlock()
}

// pollWorkflow applies the same baseline/settle/fire logic to a workflow's
// trigger; a fire starts one execution with every root job and records
// the matched files on it.
func (w *Watcher) pollWorkflow(ctx context.Context, wf store.Workflow) {
	if wf.WatchLocationID == nil {
		return
	}
	side, err := endpoints.Resolve(ctx, w.store, store.TransferEndpoint{Kind: "location", LocationID: wf.WatchLocationID})
	if err != nil || side.AgentID != nil {
		slog.Debug("workflow watch", "workflow", wf.ID, "err", err)
		return
	}
	b, err := storage.Open(ctx, side.Spec)
	if err != nil {
		slog.Debug("workflow watch open", "workflow", wf.ID, "err", err)
		return
	}
	entries, err := b.List(ctx, wf.WatchPath)
	b.Close()
	if err != nil {
		slog.Debug("workflow watch list", "workflow", wf.ID, "err", err)
		return
	}
	sig, files, _ := Signature(entries)

	w.mu.Lock()
	seen, haveSeen := w.lastSeen[wf.ID]
	w.mu.Unlock()
	if !haveSeen {
		if wf.WatchLastSig == "" {
			_ = w.store.SetWorkflowWatchSig(ctx, wf.ID, sig)
			w.mu.Lock()
			w.lastSeen[wf.ID] = sig
			w.mu.Unlock()
			slog.Info("workflow watch baseline", "workflow", wf.ID, "path", wf.WatchPath)
			return
		}
		seen = wf.WatchLastSig
		w.mu.Lock()
		w.lastSeen[wf.ID] = seen
		w.mu.Unlock()
	}
	if sig != seen {
		w.mu.Lock()
		w.lastSeen[wf.ID] = sig
		w.mu.Unlock()
		return
	}
	if sig == wf.WatchLastSig {
		return
	}
	_, runs, err := w.store.StartWorkflow(ctx, wf, store.TriggerFile, time.Now(), files)
	if err != nil {
		slog.Warn("workflow watch start", "workflow", wf.ID, "err", err)
		return
	}
	if err := w.store.SetWorkflowWatchSig(ctx, wf.ID, sig); err != nil {
		slog.Warn("workflow watch record sig", "err", err)
	}
	slog.Info("workflow watch fired", "workflow", wf.Name, "roots", len(runs), "files", len(files))
	if w.hub != nil {
		for _, run := range runs {
			w.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
				"id":          run.ID,
				"job_id":      run.JobID,
				"status":      run.Status,
				"target_type": run.TargetType,
				"trigger":     store.TriggerFile,
			})
		}
	}
}

// poll computes the current signature and applies baseline/settle/fire logic.
func (w *Watcher) poll(ctx context.Context, wj store.WatchJob) {
	sig, files, err := w.signature(ctx, wj)
	if err != nil {
		slog.Debug("watch poll", "job", wj.ID, "path", wj.WatchPath, "err", err)
		return
	}

	w.mu.Lock()
	seen, haveSeen := w.lastSeen[wj.ID]
	w.mu.Unlock()

	if !haveSeen {
		// First poll this process. Never fired before? Baseline without
		// firing so pre-existing files don't surprise-trigger.
		if wj.LastSig == "" {
			if err := w.store.SetWatchSig(ctx, wj.ID, sig); err != nil {
				slog.Warn("watch baseline", "err", err)
			}
			w.mu.Lock()
			w.lastSeen[wj.ID] = sig
			w.mu.Unlock()
			slog.Info("watch baseline", "job", wj.ID, "path", wj.WatchPath)
			return
		}
		// Resume from the DB-recorded fired signature.
		seen = wj.LastSig
		w.mu.Lock()
		w.lastSeen[wj.ID] = seen
		w.mu.Unlock()
	}

	if sig != seen {
		// Content changed — wait for it to settle across one more poll.
		w.mu.Lock()
		w.lastSeen[wj.ID] = sig
		w.mu.Unlock()
		return
	}

	// Stable across two polls: fire if it differs from the last fired set.
	if sig == wj.LastSig {
		return
	}
	job, err := w.store.GetJob(ctx, wj.ID)
	if err != nil {
		slog.Warn("watch load job", "job", wj.ID, "err", err)
		return
	}
	run, err := w.store.EnqueueWithPolicy(ctx, job, store.TriggerFile, time.Now())
	if err != nil {
		slog.Warn("watch enqueue", "job", wj.ID, "err", err)
		return
	}
	if err := w.store.SetRunTriggerFiles(ctx, run.ID, files); err != nil {
		slog.Warn("watch record files", "err", err)
	}
	if err := w.store.SetWatchSig(ctx, wj.ID, sig); err != nil {
		slog.Warn("watch record sig", "err", err)
	}
	slog.Info("watch fired", "job", job.Name, "run", run.ID, "path", wj.WatchPath, "files", len(files))
	if w.hub != nil {
		w.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
			"id":          run.ID,
			"job_id":      job.ID,
			"job_name":    job.Name,
			"status":      run.Status,
			"target_type": run.TargetType,
			"trigger":     store.TriggerFile,
		})
	}
}

// signature hashes the sorted set of matching files (name+size+mtime) and
// returns their paths.
func (w *Watcher) signature(ctx context.Context, wj store.WatchJob) (string, []string, error) {
	side, err := endpoints.WatchSide(ctx, w.store, store.Job{
		WatchTarget:     wj.WatchTarget,
		WatchSSHHostID:  wj.WatchSSHHostID,
		WatchLocationID: wj.WatchLocationID,
	})
	if err != nil {
		return "", nil, err
	}
	if side.AgentID != nil {
		return "", nil, fmt.Errorf("agent watches are polled by the agent")
	}
	b, err := storage.Open(ctx, side.Spec)
	if err != nil {
		return "", nil, err
	}
	defer b.Close()
	entries, err := b.List(ctx, wj.WatchPath)
	if err != nil {
		return "", nil, err
	}
	return Signature(entries)
}

// Signature hashes a listing the way the watcher and the agent both do.
func Signature(entries []storage.Entry) (string, []string, error) {
	lines := make([]string, 0, len(entries))
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s|%d|%d", e.Path, e.Size, e.ModTime.UnixNano()))
		files = append(files, e.Path)
	}
	sort.Strings(lines)
	sort.Strings(files)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), files, nil
}
