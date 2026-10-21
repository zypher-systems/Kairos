package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/protocol"
	"github.com/zypher-systems/kairos/internal/server/endpoints"
	"github.com/zypher-systems/kairos/internal/server/secrets"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
	"github.com/zypher-systems/kairos/internal/storage"
)

// Agent endpoints are the worker-facing surface, authenticated by
// per-agent bearer tokens.

type ctxKeyAgent struct{}

// AgentAuth verifies the bearer token and injects the agent into context.
func (a *API) AgentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token := strings.TrimPrefix(auth, prefix)
		agent, err := a.store.GetAgentByTokenHash(r.Context(), store.HashToken(token))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyAgent{}, agent)))
	})
}

func agentFrom(ctx context.Context) (store.Agent, bool) {
	a, ok := ctx.Value(ctxKeyAgent{}).(store.Agent)
	return a, ok
}

// POST /api/v1/agent/register — (re)register attributes for an issued token.
func (a *API) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	var req protocol.AgentRegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}
	name := req.Name
	if name == "" {
		name = agent.Name
	}
	// Labels route work to agents, so an agent may not choose its own: a
	// token holder could otherwise claim any selector's runs and their
	// secrets. The server's labels stand; a mismatch is only logged.
	if len(req.Labels) > 0 && !maps.Equal(req.Labels, agent.Labels) {
		slog.Warn("agent reported labels differ from the server's; server labels apply",
			"agent", agent.Name, "reported", req.Labels, "server", agent.Labels)
	}
	if err := a.store.UpdateAgentAttrs(r.Context(), agent.ID, name, req.Hostname, req.Version, req.Capabilities); err != nil {
		slog.Error("agent register", "err", err)
		writeErr(w, http.StatusInternalServerError, "registration failed")
		return
	}
	writeJSON(w, http.StatusOK, protocol.AgentInfo{
		ID:   agent.ID.String(),
		Name: name,
	})
}

// POST /api/v1/agent/heartbeat
func (a *API) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}
	if err := a.store.TouchAgentHeartbeat(r.Context(), agent.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "heartbeat failed")
		return
	}
	cancel, err := a.store.CanceledRunsForAgent(r.Context(), agent.ID)
	if err != nil {
		slog.Warn("canceled runs lookup", "err", err)
	}
	writeJSON(w, http.StatusOK, protocol.HeartbeatResponse{
		Now:    time.Now(),
		Cancel: cancel,
		Watch:  a.watchTasksFor(r, agent.ID),
	})
}

// POST /api/v1/agent/claim — long-poll for the next run assignment.
func (a *API) handleAgentClaim(w http.ResponseWriter, r *http.Request) {
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}

	lease := a.cfg.QueueLease
	deadline := time.After(25 * time.Second)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		claim, runID, found, err := a.store.ClaimNextForAgent(r.Context(), agent.ID, lease)
		if err != nil {
			slog.Error("agent claim", "err", err)
			writeErr(w, http.StatusInternalServerError, "claim failed")
			return
		}
		if found {
			spec, err := a.buildRunSpec(r.Context(), runID)
			if err != nil {
				slog.Error("build run spec", "err", err)
				code := -1
				if _, cErr := a.store.CompleteClaim(r.Context(), runID, claim.TargetID, claim.ClaimVersion, store.RunFailed, &code, err.Error()); cErr != nil {
					slog.Error("fail claim after spec", "err", cErr)
				}
				writeErr(w, http.StatusInternalServerError, "claim failed")
				return
			}
			spec.ClaimEpoch = claim.ClaimVersion
			spec.LeaseSeconds = int(lease.Seconds())
			if a.hub != nil {
				a.hub.Publish(ws.TopicActivity, "run_running", map[string]any{
					"id":          runID,
					"job_id":      spec.JobID,
					"job_name":    spec.JobName,
					"status":      store.RunRunning,
					"target_type": "agent",
				})
			}
			slog.Info("agent claimed", "agent", agent.ID, "run", runID)
			writeJSON(w, http.StatusOK, protocol.ClaimResponse{Run: spec})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		case <-deadline:
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
}

// POST /api/v1/agent/runs/{id}/logs
func (a *API) handleAgentLogs(w http.ResponseWriter, r *http.Request) {
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	held, err := a.store.RunClaimedBy(r.Context(), runID, agent.ID.String())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "log append failed")
		return
	}
	if !held {
		writeErr(w, http.StatusForbidden, "run not claimed by this agent")
		return
	}
	var batch protocol.LogBatch
	if err := decodeJSON(r, &batch); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	chunks := make([]store.LogChunk, 0, len(batch.Chunks))
	for _, c := range batch.Chunks {
		chunks = append(chunks, store.LogChunk{Seq: c.Seq, Stream: c.Stream, Data: c.Data})
	}
	accepted, err := a.store.AppendLogs(r.Context(), runID, chunks)
	if err != nil && !errors.Is(err, store.ErrLogCapped) {
		writeErr(w, http.StatusInternalServerError, "log append failed")
		return
	}
	if a.hub != nil {
		for _, c := range chunks[:accepted] {
			a.hub.Publish(ws.RunTopic(runID.String()), "log", map[string]any{
				"seq":    c.Seq,
				"stream": c.Stream,
				"data":   store.SanitizeLogData(c.Data, store.MaxLogChunkBytes),
			})
		}
	}
	// A capped run answers 204 too: the agent has nothing to resend.
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/agent/runs/{id}/lease — extend lease while work continues.
func (a *API) handleAgentLease(w http.ResponseWriter, r *http.Request) {
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	// Agents from before 1.15 renew with an empty body and no epoch.
	req := protocol.LeaseRequest{ClaimEpoch: store.EpochUnknown}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json body")
			return
		}
	}
	if err := a.store.ExtendLease(r.Context(), runID, agent.ID.String(), req.ClaimEpoch, a.cfg.QueueLease); err != nil {
		if err == store.ErrStaleClaim {
			writeErr(w, http.StatusConflict, "stale claim")
			return
		}
		writeErr(w, http.StatusInternalServerError, "lease extend failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/agent/runs/{id}/complete
func (a *API) handleAgentComplete(w http.ResponseWriter, r *http.Request) {
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	var req protocol.CompleteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	// An agent going down hands the run back rather than failing it.
	if req.Status == protocol.StatusReleased {
		err := a.store.ReleaseClaim(r.Context(), runID, agent.ID.String(), req.ClaimEpoch)
		if errors.Is(err, store.ErrStaleClaim) {
			writeErr(w, http.StatusConflict, "stale claim")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "release failed")
			return
		}
		slog.Info("agent released run", "agent", agent.ID, "run", runID)
		if a.hub != nil {
			a.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
				"id":          runID,
				"status":      store.RunQueued,
				"target_type": "agent",
			})
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	status := store.RunSucceeded
	if req.Status == "failed" || req.Status == "canceled" {
		status = req.Status
	}
	res, err := a.store.CompleteClaim(r.Context(), runID, agent.ID.String(), req.ClaimEpoch, status, req.ExitCode, req.Error)
	if err != nil {
		if errors.Is(err, store.ErrStaleClaim) {
			writeErr(w, http.StatusConflict, "stale claim")
			return
		}
		writeErr(w, http.StatusInternalServerError, "complete failed")
		return
	}
	if res.Retry != nil {
		slog.Info("retry scheduled", "run", runID, "attempt", res.Retry.Attempt)
	}

	if a.hub != nil {
		if res.Retry != nil {
			a.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
				"id":          res.Retry.ID,
				"job_id":      res.Retry.JobID,
				"status":      res.Retry.Status,
				"target_type": res.Retry.TargetType,
				"trigger":     store.TriggerRetry,
			})
		}
		a.hub.Publish(ws.TopicActivity, "run_finished", map[string]any{
			"id":          runID,
			"job_id":      res.Run.JobID,
			"status":      res.Run.Status,
			"exit_code":   res.Run.ExitCode,
			"target_type": "agent",
		})
		for _, child := range res.Downstream {
			a.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
				"id":          child.ID,
				"job_id":      child.JobID,
				"status":      child.Status,
				"target_type": child.TargetType,
				"trigger":     store.TriggerWorkflow,
			})
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// buildRunSpec loads the job and constructs the payload for the agent.
func (a *API) buildRunSpec(ctx context.Context, runID uuid.UUID) (*protocol.RunSpec, error) {
	run, err := a.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	job, err := a.store.GetJob(ctx, run.JobID)
	if err != nil {
		return nil, err
	}
	if err := a.store.RevealJobSecrets(&job); err != nil {
		return nil, err
	}
	if err := secrets.Resolve(ctx, a.store, &job); err != nil {
		return nil, err
	}
	a.store.OverlayWorkflowParams(ctx, &job, run.WorkflowExecutionID)
	env := job.MergedEnv()
	secretKeys := make([]string, 0, len(job.EnvSecrets))
	for k := range job.EnvSecrets {
		secretKeys = append(secretKeys, k)
	}
	spec := &protocol.RunSpec{
		RunID:       run.ID,
		JobID:       job.ID,
		JobName:     job.Name,
		ExecMode:    job.ExecMode,
		DockerImage: job.DockerImage,
		DockerArgs:  job.DockerCommand,
		ShellScript: job.ShellScript,
		Workdir:     job.Workdir,
		Env:         env,
		SecretKeys:  secretKeys,
		TimeoutSec:  job.TimeoutSeconds,
	}
	if len(run.TriggerFiles) > 0 {
		spec.Env["KAIROS_TRIGGER_FILES"] = strings.Join(run.TriggerFiles, "\n")
		spec.Env["KAIROS_TRIGGER_FILE"] = run.TriggerFiles[0]
	}
	if job.ExecMode == "transfer" {
		ts, err := buildAgentTransferSpec(ctx, a.store, job, run)
		if err != nil {
			return nil, err
		}
		spec.Transfer = ts
		for _, side := range []protocol.TransferSide{ts.Source, ts.Dest} {
			if side.Storage != nil {
				for i, v := range side.Storage.Secrets() {
					k := fmt.Sprintf("KAIROS_STORAGE_SECRET_%d", len(spec.SecretKeys)+i)
					spec.Env[k] = v
					spec.SecretKeys = append(spec.SecretKeys, k)
				}
			}
		}
	}
	return spec, nil
}

// buildAgentTransferSpec maps a job's transfer route onto the agent
// channel: each side becomes the library (over the agent API) or a storage
// spec the agent opens itself. Storage credentials travel in the spec; they
// are also listed as secret keys so the agent redacts them from logs.
func buildAgentTransferSpec(ctx context.Context, st *store.Store, job store.Job, run store.Run) (*protocol.TransferSpec, error) {
	c := job.TransferConfig
	src, err := endpoints.Resolve(ctx, st, c.Source)
	if err != nil {
		return nil, fmt.Errorf("transfer source: %w", err)
	}
	if c.FromTrigger {
		src, err = endpoints.WatchSide(ctx, st, job)
		if err != nil {
			return nil, fmt.Errorf("transfer trigger source: %w", err)
		}
	}
	dst, err := endpoints.Resolve(ctx, st, c.Dest)
	if err != nil {
		return nil, fmt.Errorf("transfer destination: %w", err)
	}
	ts := &protocol.TransferSpec{
		Overwrite:      c.Overwrite,
		DeleteSource:   c.DeleteSource,
		PreserveMTime:  c.PreserveMTime,
		VerifyChecksum: c.VerifyChecksum,
	}
	if c.FromTrigger {
		ts.Files = run.TriggerFiles
	}
	toSide := func(r endpoints.Resolved, e store.TransferEndpoint, isDest bool) (protocol.TransferSide, error) {
		if r.Library {
			side := protocol.TransferSide{Library: true, Name: "library"}
			if !isDest {
				if e.FileID == nil {
					return side, errors.New("library source has no file")
				}
				f, err := st.GetFile(ctx, *e.FileID)
				if err != nil {
					return side, errors.New("library file not found")
				}
				side.FileID = f.ID.String()
				side.FileName = f.Name
			} else {
				side.FileName = filepath.Base(e.Path)
				if side.FileName == "" || side.FileName == "." || side.FileName == "/" {
					side.FileName = ""
				}
			}
			return side, nil
		}
		spec := r.Spec
		return protocol.TransferSide{Storage: &spec, Path: r.Path, Name: r.Name}, nil
	}
	if ts.Source, err = toSide(src, c.Source, false); err != nil {
		return nil, err
	}
	if ts.Dest, err = toSide(dst, c.Dest, true); err != nil {
		return nil, err
	}
	// Pre-1.16 agents understand only library<->agent path moves.
	switch {
	case ts.Source.Library && ts.Dest.Storage != nil && ts.Dest.Storage.Kind == storage.KindLocal && ts.Dest.Storage.Root == "":
		ts.Direction, ts.FileID, ts.RemotePath = "push", ts.Source.FileID, ts.Dest.Path
	case ts.Dest.Library && ts.Source.Storage != nil && ts.Source.Storage.Kind == storage.KindLocal && ts.Source.Storage.Root == "":
		ts.Direction, ts.RemotePath, ts.FileName = "pull", ts.Source.Path, ts.Dest.FileName
		if ts.FileName == "" {
			ts.FileName = filepath.Base(ts.Source.Path)
		}
	}
	return ts, nil
}

func parseRunID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id")
		return uuid.Nil, false
	}
	return id, true
}
