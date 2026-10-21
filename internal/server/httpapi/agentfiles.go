package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/protocol"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
)

// POST /api/v1/agent/watch-event — an agent reports a settled file-arrival
// on its filesystem. The server enqueues the job's run with trigger=file.
func (a *API) handleAgentWatchEvent(w http.ResponseWriter, r *http.Request) {
	var in protocol.WatchEvent
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}
	job, err := a.store.GetJob(r.Context(), in.JobID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load job")
		return
	}
	// The event must come from the agent this watch is assigned to: the
	// job's agent target, or the agent that owns the watched location.
	assigned := false
	if job.WatchEnabled {
		switch job.WatchTarget {
		case "agent":
			assigned = job.WatchAgentID != nil && *job.WatchAgentID == agent.ID
		case "location":
			if job.WatchLocationID != nil {
				if loc, err := a.store.GetStorageLocation(r.Context(), *job.WatchLocationID); err == nil {
					assigned = loc.Kind == "agent" && loc.AgentID != nil && *loc.AgentID == agent.ID
				}
			}
		}
	}
	if !assigned {
		writeErr(w, http.StatusForbidden, "watch not assigned to this agent")
		return
	}
	// De-dupe: same signature as last fire means nothing new.
	if job.WatchLastSig == in.Sig {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	run, err := a.store.EnqueueWithPolicy(r.Context(), job, store.TriggerFile, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to enqueue run")
		return
	}
	if err := a.store.SetRunTriggerFiles(r.Context(), run.ID, in.Files); err != nil {
		slog.Warn("watch files persist", "err", err)
	}
	if err := a.store.SetWatchSig(r.Context(), job.ID, in.Sig); err != nil {
		slog.Warn("watch sig persist", "err", err)
	}
	if a.hub != nil {
		a.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
			"id":          run.ID,
			"job_id":      job.ID,
			"job_name":    job.Name,
			"status":      run.Status,
			"target_type": run.TargetType,
			"trigger":     store.TriggerFile,
		})
	}
	slog.Info("agent watch fired", "job", job.Name, "agent", agent.Name, "run", run.ID)
	w.WriteHeader(http.StatusAccepted)
}

// GET /api/v1/agent/files/{id}/content — agent downloads a library file
// (push direction). Token-authenticated.
func (a *API) handleAgentFileDownload(w http.ResponseWriter, r *http.Request) {
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	held, err := a.store.AgentHoldsFile(r.Context(), agent.ID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	if !held {
		writeErr(w, http.StatusForbidden, "file not assigned to this agent")
		return
	}
	f, err := a.store.GetFile(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	p := filepath.Join(a.cfg.DataDir, f.StoragePath)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, p)
}

// POST /api/v1/agent/files — agent uploads a file into the library
// (pull direction). Query: name, run_id. The run must be a transfer into
// the library that this agent currently holds; the body is capped like a
// browser upload.
func (a *API) handleAgentFileUpload(w http.ResponseWriter, r *http.Request) {
	agent, ok := agentFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no agent")
		return
	}
	runID, err := uuid.Parse(r.URL.Query().Get("run_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "run_id is required")
		return
	}
	held, err := a.store.RunClaimedBy(r.Context(), runID, agent.ID.String())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load run")
		return
	}
	if !held {
		writeErr(w, http.StatusForbidden, "run not claimed by this agent")
		return
	}
	run, err := a.store.GetRun(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load run")
		return
	}
	job, err := a.store.GetJob(r.Context(), run.JobID)
	if err != nil || job.ExecMode != "transfer" || job.TransferConfig.Dest.Kind != "library" {
		writeErr(w, http.StatusForbidden, "run is not a transfer into the library")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	name := filepath.Base(r.URL.Query().Get("name"))
	if name == "" || name == "." || name == "/" {
		name = "agent-download"
	}
	if err := os.MkdirAll(a.filesDir(), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	rel := filepath.Join("files", uuid.NewString())
	dst := filepath.Join(a.cfg.DataDir, rel)
	fh, err := os.Create(dst)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(fh, h), r.Body)
	closeErr := fh.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		var tooBig *http.MaxBytesError
		if errors.As(copyErr, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "upload exceeds 2 GiB")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to store upload")
		return
	}
	entry := store.FileEntry{
		Name:        name,
		SizeBytes:   n,
		SHA256:      hex.EncodeToString(h.Sum(nil)),
		StoragePath: rel,
	}
	if err := a.store.CreateFile(r.Context(), &entry); err != nil {
		_ = os.Remove(dst)
		writeErr(w, http.StatusInternalServerError, "failed to register file")
		return
	}
	slog.Info("agent uploaded library file", "name", name, "bytes", n, "run", runID, "agent", agent.Name)
	writeJSON(w, http.StatusCreated, entry)
}

// watchTasksFor returns the watch assignments an agent should run, delivered
// on heartbeat.
func (a *API) watchTasksFor(r *http.Request, agentID uuid.UUID) []protocol.WatchTask {
	jobs, _, err := a.store.ListJobs(r.Context(), nil, "", 500, 0)
	if err != nil {
		return nil
	}
	var out []protocol.WatchTask
	for _, j := range jobs {
		if !j.WatchEnabled {
			continue
		}
		switch j.WatchTarget {
		case "agent":
			if j.WatchAgentID != nil && *j.WatchAgentID == agentID {
				out = append(out, protocol.WatchTask{JobID: j.ID, Glob: j.WatchPath, Interval: j.WatchIntervalSec})
			}
		case "location":
			if j.WatchLocationID == nil {
				continue
			}
			loc, err := a.store.GetStorageLocation(r.Context(), *j.WatchLocationID)
			if err != nil || loc.Kind != "agent" || loc.AgentID == nil || *loc.AgentID != agentID {
				continue
			}
			out = append(out, protocol.WatchTask{JobID: j.ID, Glob: j.WatchPath, Interval: j.WatchIntervalSec, Root: loc.BasePath})
		}
	}
	return out
}
