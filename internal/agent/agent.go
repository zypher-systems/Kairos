package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/zypher-systems/kairos/internal/protocol"
)

// Agent orchestrates registration, heartbeats, and the claim/execute loop.
//
// Every run is fenced by its claim: the agent renews the lease at a third of
// the server's lease and kills the run when a renew answers "stale" or when
// nothing has been renewed for a whole lease, because by then the server has
// requeued the run for someone else. Results are reported until the server
// accepts them or says the claim is stale, so a blip never loses a result
// and never causes a finished run to execute again.
//
// SIGTERM stops claiming and waits up to the drain timeout for running jobs.
// Jobs still running after that are killed and released back to the queue.
type Agent struct {
	cfg     *Config
	client  *Client
	version string
	watcher *Watcher

	mu       sync.Mutex
	running  map[string]context.CancelFunc // runID -> cancel
	canceled map[string]bool               // runID -> canceled server-side
	fenced   map[string]string             // runID -> why the claim was lost

	// execCtx outlives the claim loop: it is canceled when the drain
	// window closes. execWG counts runs in flight.
	execCtx    context.Context
	cancelExec context.CancelFunc
	execWG     sync.WaitGroup
}

// New builds the agent runtime.
func New(cfg *Config, version string) (*Agent, error) {
	client := NewClient(cfg.ServerURL, cfg.Token, version)
	execCtx, cancelExec := context.WithCancel(context.Background())
	return &Agent{
		cfg:        cfg,
		client:     client,
		version:    version,
		watcher:    NewWatcher(client),
		running:    make(map[string]context.CancelFunc),
		canceled:   make(map[string]bool),
		fenced:     make(map[string]string),
		execCtx:    execCtx,
		cancelExec: cancelExec,
	}, nil
}

// Run blocks until ctx is done: registers, heartbeats, and claims work.
// After ctx is done it drains running jobs and returns once every run has
// been reported or released.
func (a *Agent) Run(ctx context.Context) error {
	agentID, err := a.registerLoop(ctx)
	if err != nil {
		return err
	}
	slog.Info("registered", "agent_id", agentID)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a.heartbeatLoop(ctx, agentID) }()
	go func() { defer wg.Done(); a.claimLoop(ctx, agentID) }()
	<-ctx.Done()
	wg.Wait()

	a.drain()
	slog.Info("agent stopped")
	return nil
}

// drain waits for in-flight runs, then kills and releases the stragglers.
func (a *Agent) drain() {
	finished := make(chan struct{})
	go func() {
		a.execWG.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	default:
		slog.Info("draining running jobs", "wait", a.cfg.DrainTimeout.String(), "running", a.load())
		select {
		case <-finished:
		case <-time.After(a.cfg.DrainTimeout):
			slog.Warn("drain window closed; releasing running jobs to the queue", "running", a.load())
		}
	}
	a.cancelExec()
	<-finished
}

func (a *Agent) registerLoop(ctx context.Context) (string, error) {
	backoff := time.Second
	for {
		id, err := a.client.Register(ctx, a.cfg.Name, a.cfg.Hostname, Labels())
		if err == nil {
			return id, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoff):
		}
		slog.Warn("register failed, retrying", "err", err, "backoff", backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) heartbeatLoop(ctx context.Context, agentID string) {
	t := time.NewTicker(a.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			resp, err := a.client.Heartbeat(ctx, agentID, a.load())
			if err != nil {
				slog.Warn("heartbeat failed", "err", err)
				continue
			}
			if len(resp.Cancel) > 0 {
				slog.Info("server canceled runs", "runs", resp.Cancel)
				a.kill(resp.Cancel)
			}
			a.watcher.Sync(ctx, resp.Watch)
		}
	}
}

// load is the number of runs in flight.
func (a *Agent) load() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.running)
}

// kill cancels the given runs; their executors report canceled state.
func (a *Agent) kill(runIDs []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range runIDs {
		if cancel, ok := a.running[id]; ok {
			a.canceled[id] = true
			cancel()
		}
	}
}

// fence kills a run whose claim the server no longer honours.
func (a *Agent) fence(runID, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cancel, ok := a.running[runID]; ok {
		a.fenced[runID] = reason
		cancel()
	}
}

func (a *Agent) track(runID string, cancel context.CancelFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running[runID] = cancel
}

func (a *Agent) untrack(runID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.running, runID)
	delete(a.canceled, runID)
	delete(a.fenced, runID)
}

func (a *Agent) claimLoop(ctx context.Context, agentID string) {
	sem := make(chan struct{}, a.cfg.MaxConcurrent)
	backoff := 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		run, err := a.client.Claim(ctx, agentID)
		if err != nil {
			<-sem
			if ctx.Err() != nil {
				return
			}
			slog.Warn("claim failed", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter(backoff)):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 2 * time.Second
		if run == nil {
			<-sem
			continue // long-poll expired: loop again immediately
		}
		a.execWG.Add(1)
		go func() {
			defer func() { <-sem; a.execWG.Done() }()
			a.executeRun(run)
		}()
	}
}

// jitter spreads retries so a fleet does not hammer a recovering server.
func jitter(d time.Duration) time.Duration {
	return d/2 + time.Duration(time.Now().UnixNano()%int64(d/2+1))
}

// executeRun drives one claimed run: streams logs, executes, reports. The
// run's context descends from execCtx, not the claim loop's, so a shutdown
// drains it instead of killing it at once.
func (a *Agent) executeRun(spec *protocol.RunSpec) {
	runID := spec.RunID.String()
	slog.Info("executing run", "run", spec.RunID, "job", spec.JobName, "mode", spec.ExecMode)

	runCtx, cancel := context.WithCancel(a.execCtx)
	defer cancel()
	a.track(runID, cancel)
	defer a.untrack(runID)

	logs := make(chan protocol.LogChunk, 4096)
	runner := NewRunner(logs, a.client)
	runner.RedactSecrets(spec)

	// pump persists chunks to the server.
	flushed := make(chan struct{})
	pumpCtx, cancelPump := context.WithCancel(context.Background())
	go func() {
		defer close(flushed)
		a.pumpLogs(pumpCtx, runID, logs)
	}()

	// lease renewer keeps the claim alive during long runs and fences it.
	stopRenew := a.renewLease(runCtx, spec)

	runner.Emit("system", fmt.Sprintf("=== kairos run %s · job %q ===", spec.RunID, spec.JobName))
	status, exit, errMsg := runner.Execute(runCtx, spec)
	stopRenew()

	a.mu.Lock()
	canceled := a.canceled[runID]
	fenced := a.fenced[runID]
	a.mu.Unlock()
	shutdown := a.execCtx.Err() != nil && !canceled && fenced == ""

	switch {
	case fenced != "":
		runner.Emit("system", "[kairos] run stopped: claim lost ("+fenced+")")
	case shutdown:
		status = protocol.StatusReleased
		runner.Emit("system", "[kairos] agent shutting down; the run returns to the queue and restarts on another worker")
	case canceled:
		status = "canceled"
		if exit == nil {
			c := -1
			exit = &c
		}
		errMsg = "canceled by operator"
	}

	close(logs)
	select {
	case <-flushed:
	case <-time.After(30 * time.Second):
		slog.Warn("log flush timed out", "run", spec.RunID)
	}
	cancelPump()

	if fenced != "" {
		// The server already gave the run away; the result belongs to nobody.
		slog.Warn("claim lost during run", "run", spec.RunID, "reason", fenced)
		return
	}
	a.report(runID, status, exit, errMsg, spec.ClaimEpoch)
}

// report delivers the result until the server accepts it or declares the
// claim stale. It uses its own context: the result must land even while
// the agent is shutting down.
func (a *Agent) report(runID, status string, exit *int, errMsg string, epoch int) {
	backoff := time.Second
	deadline := time.Now().Add(10 * time.Minute)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := a.client.Complete(ctx, runID, status, exit, errMsg, epoch)
		cancel()
		if err == nil {
			slog.Info("run finished", "run", runID, "status", status)
			return
		}
		if errors.Is(err, ErrStaleClaim) {
			slog.Warn("run result rejected: claim is stale", "run", runID, "status", status)
			return
		}
		var se *StatusError
		if errors.As(err, &se) && se.Permanent() {
			slog.Error("run result rejected", "run", runID, "err", err)
			return
		}
		if time.Now().After(deadline) {
			slog.Error("giving up reporting run result; the server will requeue it when the lease lapses", "run", runID, "err", err)
			return
		}
		slog.Warn("complete failed, retrying", "run", runID, "err", err, "backoff", backoff)
		time.Sleep(jitter(backoff))
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// renewLease extends the claim on a cadence derived from the server's lease
// and fences the run when the claim is gone or could not be renewed within
// the lease.
func (a *Agent) renewLease(ctx context.Context, spec *protocol.RunSpec) func() {
	runID := spec.RunID.String()
	lease := time.Duration(spec.LeaseSeconds) * time.Second
	interval := a.cfg.LeaseRenew
	if lease > 0 {
		interval = lease / 3
		if interval > 10*time.Second {
			interval = 10 * time.Second
		}
		if interval < time.Second {
			interval = time.Second
		}
	} else {
		lease = 3 * interval
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		lastOK := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
			}
			cctx, cancel := context.WithTimeout(context.Background(), interval)
			err := a.client.RenewLease(cctx, runID, spec.ClaimEpoch)
			cancel()
			switch {
			case err == nil:
				lastOK = time.Now()
				continue
			case errors.Is(err, ErrStaleClaim):
				a.fence(runID, "claim no longer held by this agent")
				return
			case time.Since(lastOK) >= lease:
				a.fence(runID, "lease expired without renewal: "+err.Error())
				return
			default:
				slog.Warn("lease renew failed", "run", runID, "err", err)
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// pumpLogs ships log chunks in batches. A batch the server rejects for good
// (4xx) is dropped so one bad chunk cannot block every later line; other
// failures retry a few times before the batch is dropped.
func (a *Agent) pumpLogs(ctx context.Context, runID string, logs <-chan protocol.LogChunk) {
	var batch []protocol.LogChunk
	failures := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := a.client.SendLogs(sendCtx, runID, batch)
		cancel()
		if err == nil {
			batch = batch[:0]
			failures = 0
			return
		}
		failures++
		var se *StatusError
		if (errors.As(err, &se) && se.Permanent()) || failures >= 3 {
			slog.Warn("log batch dropped", "run", runID, "chunks", len(batch), "err", err)
			batch = batch[:0]
			failures = 0
			return
		}
		slog.Warn("log upload failed, will retry", "run", runID, "err", err)
	}
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case c, ok := <-logs:
			if !ok {
				flush()
				if len(batch) > 0 {
					flush()
				}
				return
			}
			batch = append(batch, c)
			if len(batch) >= 64 {
				flush()
			}
		case <-t.C:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}
