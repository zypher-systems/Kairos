package exec

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/metrics"
	"github.com/zypher-systems/kairos/internal/server/notify"
	"github.com/zypher-systems/kairos/internal/server/secrets"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
)

// Dispatcher claims queued local/ssh runs and executes them with a bounded
// worker pool. Agent-targeted runs are claimed by agents themselves.
//
// Every run is fenced by its claim: the worker renews the lease on a short
// interval and kills the process the moment a renew finds the claim gone
// (canceled, or requeued after the lease lapsed) or the lease could not be
// renewed before it expired. A superseded worker therefore never runs next
// to its replacement, and a canceled run stops rather than merely showing
// as canceled.
//
// Shutdown stops claiming, then waits up to drain for in-flight runs. Runs
// still going after that are killed and handed back to the queue with their
// attempt intact, so a restart resumes them instead of failing them.
type Dispatcher struct {
	store     *store.Store
	local     *LocalRunner
	ssh       *SSHRunner
	transfer  *TransferRunner
	hub       *ws.Hub
	notifier  *notify.Notifier
	maxConcur int
	tick      time.Duration
	lease     time.Duration
	drain     time.Duration
	wg        sync.WaitGroup

	// execCtx outlives the claim loop's context: it is canceled only when
	// the drain window closes.
	execCtx    context.Context
	cancelExec context.CancelFunc
}

// NewDispatcher builds the dispatcher. dataDir roots the managed file
// library used by transfer jobs. drain is how long shutdown waits for
// in-flight runs before handing them back to the queue.
func NewDispatcher(st *store.Store, hub *ws.Hub, notifier *notify.Notifier, maxConcur int, tick, lease, drain time.Duration, dataDir string) *Dispatcher {
	if maxConcur <= 0 {
		maxConcur = 8
	}
	if tick <= 0 {
		tick = time.Second
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if drain < 0 {
		drain = 0
	}
	execCtx, cancelExec := context.WithCancel(context.Background())
	return &Dispatcher{
		store:      st,
		local:      NewLocalRunner(st),
		ssh:        NewSSHRunner(st),
		transfer:   NewTransferRunner(st, dataDir),
		hub:        hub,
		notifier:   notifier,
		maxConcur:  maxConcur,
		tick:       tick,
		lease:      lease,
		drain:      drain,
		execCtx:    execCtx,
		cancelExec: cancelExec,
	}
}

// Run claims work until ctx is done, then drains: in-flight runs get up to
// the drain window to finish, after which they are killed and released
// back to the queue. Run returns once every worker has reported.
func (d *Dispatcher) Run(ctx context.Context) {
	if err := d.store.ForceRequeueDispatcherOrphaned(ctx); err != nil {
		slog.Warn("orphan sweep", "err", err)
	}

	sem := make(chan struct{}, d.maxConcur)
	t := time.NewTicker(d.tick)
	defer t.Stop()
	slog.Info("dispatcher started", "concurrency", d.maxConcur, "tick", d.tick.String(), "lease", d.lease.String(), "drain", d.drain.String())

	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-t.C:
			d.pump(ctx, sem)
		}
	}

	finished := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	default:
		slog.Info("dispatcher draining", "wait", d.drain.String())
		select {
		case <-finished:
		case <-time.After(d.drain):
			slog.Warn("dispatcher drain window closed; releasing in-flight runs")
		}
	}
	d.cancelExec()
	<-finished
	slog.Info("dispatcher stopped")
}

// pump claims up to the free capacity and spawns workers.
func (d *Dispatcher) pump(ctx context.Context, sem chan struct{}) {
	for {
		select {
		case sem <- struct{}{}:
		default:
			return // at capacity
		}
		run, job, claim, ok, err := d.claim(ctx)
		if err != nil {
			slog.Warn("claim error", "err", err)
		}
		if !ok {
			<-sem
			return
		}
		d.wg.Add(1)
		go func() {
			defer func() { <-sem; d.wg.Done() }()
			d.execute(run, job, claim)
		}()
	}
}

// claim atomically picks the oldest queued local/ssh run and leases it.
func (d *Dispatcher) claim(ctx context.Context) (store.Run, store.Job, store.QueueClaim, bool, error) {
	c, run, ok, err := d.store.ClaimNextDispatcher(ctx, d.lease)
	if err != nil || !ok {
		return store.Run{}, store.Job{}, store.QueueClaim{}, false, err
	}
	job, err := d.store.GetJob(ctx, run.JobID)
	if err != nil {
		return store.Run{}, store.Job{}, store.QueueClaim{}, false, err
	}
	// A secret that cannot be revealed or resolved fails the run here, with
	// the reason in the run, instead of leaving the claim to lapse and the
	// run to bounce between workers.
	if err := d.store.RevealJobSecrets(&job); err != nil {
		d.failClaim(ctx, run, c, err)
		return store.Run{}, store.Job{}, store.QueueClaim{}, false, nil
	}
	if err := secrets.Resolve(ctx, d.store, &job); err != nil {
		d.failClaim(ctx, run, c, err)
		return store.Run{}, store.Job{}, store.QueueClaim{}, false, nil
	}
	d.store.OverlayWorkflowParams(ctx, &job, run.WorkflowExecutionID)
	return run, job, c, true, nil
}

// failClaim records a run that could not start, with the reason.
func (d *Dispatcher) failClaim(ctx context.Context, run store.Run, c store.QueueClaim, cause error) {
	code := -1
	res, err := d.store.CompleteClaim(ctx, run.ID, c.TargetID, c.ClaimVersion, store.RunFailed, &code, cause.Error())
	if err != nil {
		slog.Warn("fail claim", "run", run.ID, "err", err)
		return
	}
	metrics.RunsFinished.Inc(store.RunFailed)
	job, jerr := d.store.GetJob(ctx, run.JobID)
	if jerr == nil {
		d.publishCompletion(job, res)
	}
	slog.Warn("run failed before start", "run", run.ID, "err", cause)
}

// execute runs one claimed run to completion.
func (d *Dispatcher) execute(run store.Run, job store.Job, claim store.QueueClaim) {
	runLogger := slog.With("run", run.ID, "job", job.ID, "name", job.Name)
	runLogger.Info("dispatch", "target", run.TargetType, "trigger", run.Trigger)

	dbCtx, cancelDB := context.WithTimeout(context.Background(), 15*time.Second)
	started, err := d.store.MarkRunRunning(dbCtx, run.ID)
	cancelDB()
	if err != nil {
		runLogger.Warn("mark running", "err", err)
		return
	}
	if !started {
		// Canceled between claim and start: nothing to run and nothing to
		// complete, the cancel already dropped the claim.
		runLogger.Info("run canceled before start")
		return
	}
	metrics.RunsStarted.Inc(run.TargetType)
	if d.hub != nil {
		d.hub.Publish(ws.TopicActivity, "run_running", map[string]any{
			"id":          run.ID,
			"job_id":      run.JobID,
			"job_name":    job.Name,
			"status":      store.RunRunning,
			"target_type": run.TargetType,
		})
	}

	rctx, cancel := context.WithTimeout(d.execCtx, time.Duration(job.TimeoutSeconds)*time.Second)
	defer cancel()

	sink := NewLogSink(d.execCtx, d.store, run.ID, d.hub)
	sink.Redact(secretValues(job))
	sink.System("=== kairos run %s · job %q (%s) ===", run.ID, job.Name, run.Trigger)

	fence := d.renewLease(rctx, cancel, run.ID, claim)
	defer fence.stop()

	// A file-triggered run tells the job what arrived.
	if len(run.TriggerFiles) > 0 {
		if job.EnvPlain == nil {
			job.EnvPlain = map[string]string{}
		}
		job.EnvPlain["KAIROS_TRIGGER_FILES"] = strings.Join(run.TriggerFiles, "\n")
		job.EnvPlain["KAIROS_TRIGGER_FILE"] = run.TriggerFiles[0]
	}

	var exit int
	var runErr string
	if job.ExecMode == "transfer" {
		exit, runErr = d.transfer.Execute(rctx, job, run, sink)
	} else {
		switch job.TargetType {
		case "local":
			exit, runErr = d.local.Execute(rctx, job, shortID(run.ID), sink)
		case "ssh":
			exit, runErr = d.ssh.Execute(rctx, job, shortID(run.ID), sink)
		default:
			exit, runErr = -1, "unknown target type"
		}
	}
	fence.stop()

	dbCtx, cancelDB = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelDB()

	switch {
	case fence.lost():
		// The claim is gone: the run was canceled, or the lease lapsed and
		// the queue handed the run to someone else. Either way the result
		// belongs to nobody now.
		sink.System("[kairos] run stopped: claim lost (%s)", fence.reason())
		sink.Close()
		metrics.RunsFenced.Inc()
		runLogger.Warn("claim lost during run", "reason", fence.reason())
		return
	case d.execCtx.Err() != nil && rctx.Err() != nil:
		// Killed by shutdown, not by its own timeout: give it back.
		sink.System("[kairos] server shutting down; the run returns to the queue and restarts on another worker")
		sink.Close()
		if err := d.store.ReleaseClaim(dbCtx, run.ID, claim.TargetID, claim.ClaimVersion); err != nil {
			runLogger.Warn("release run on shutdown", "err", err)
			return
		}
		metrics.RunsRequeued.Inc("shutdown")
		runLogger.Info("released to queue on shutdown")
		return
	}

	code := exit
	status := store.RunSucceeded
	if runErr != "" || exit != 0 {
		status = store.RunFailed
	}
	sink.Close()

	res, err := d.store.CompleteClaim(dbCtx, run.ID, claim.TargetID, claim.ClaimVersion, status, &code, runErr)
	if err != nil {
		if errors.Is(err, store.ErrStaleClaim) {
			runLogger.Warn("complete run: claim was superseded")
		} else {
			runLogger.Warn("complete run", "err", err)
		}
		return
	}
	metrics.RunsFinished.Inc(res.Run.Status)
	d.publishCompletion(job, res)
	runLogger.Info("finished", "status", res.Run.Status, "exit", exit)
}

// publishCompletion fans the completion and what it enqueued out to the UI.
func (d *Dispatcher) publishCompletion(job store.Job, res store.CompleteResult) {
	if d.hub == nil {
		return
	}
	if res.Retry != nil {
		d.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
			"id":          res.Retry.ID,
			"job_id":      job.ID,
			"job_name":    job.Name,
			"status":      res.Retry.Status,
			"target_type": res.Retry.TargetType,
			"trigger":     store.TriggerRetry,
		})
	}
	d.hub.Publish(ws.TopicActivity, "run_finished", map[string]any{
		"id":          res.Run.ID,
		"job_id":      res.Run.JobID,
		"job_name":    job.Name,
		"status":      res.Run.Status,
		"exit_code":   res.Run.ExitCode,
		"target_type": res.Run.TargetType,
	})
	for _, child := range res.Downstream {
		d.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
			"id":          child.ID,
			"job_id":      child.JobID,
			"status":      child.Status,
			"target_type": child.TargetType,
			"trigger":     store.TriggerWorkflow,
		})
	}
}

// secretValues lists the job's secret values so the sink can redact them.
func secretValues(job store.Job) []string {
	out := make([]string, 0, len(job.EnvSecrets))
	for _, v := range job.EnvSecrets {
		out = append(out, v)
	}
	return out
}

func shortID(id uuid.UUID) string {
	s := id.String()
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// leaseFence renews a claim and kills the run when the claim is lost.
type leaseFence struct {
	mu     sync.Mutex
	lostBy string
	done   chan struct{}
	once   sync.Once
}

func (f *leaseFence) stop() { f.once.Do(func() { close(f.done) }) }

func (f *leaseFence) lost() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lostBy != ""
}

func (f *leaseFence) reason() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lostBy
}

// renewLease extends the claim every renewInterval and calls kill when the
// claim is gone or has not been renewed within the lease. The renew cadence
// is short so a cancel reaches the process within a few seconds.
func (d *Dispatcher) renewLease(ctx context.Context, kill context.CancelFunc, runID uuid.UUID, claim store.QueueClaim) *leaseFence {
	f := &leaseFence{done: make(chan struct{})}
	interval := d.lease / 3
	if interval > 3*time.Second {
		interval = 3 * time.Second
	}
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		lastOK := time.Now()
		for {
			select {
			case <-f.done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
			}
			rctx, cancel := context.WithTimeout(context.Background(), interval)
			err := d.store.ExtendLease(rctx, runID, claim.TargetID, claim.ClaimVersion, d.lease)
			cancel()
			switch {
			case err == nil:
				lastOK = time.Now()
				continue
			case errors.Is(err, store.ErrStaleClaim):
				f.mu.Lock()
				f.lostBy = "claim no longer held"
				f.mu.Unlock()
			case time.Since(lastOK) >= d.lease:
				f.mu.Lock()
				f.lostBy = "lease expired without renewal: " + err.Error()
				f.mu.Unlock()
			default:
				slog.Warn("dispatcher lease renew", "run", runID, "err", err)
				continue
			}
			slog.Warn("dispatcher fencing run", "run", runID, "reason", f.reason())
			kill()
			return
		}
	}()
	return f
}
