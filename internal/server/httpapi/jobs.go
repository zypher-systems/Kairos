package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/scheduler"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
)

func (a *API) handleListJobs(w http.ResponseWriter, r *http.Request) {
	var enabled *bool
	if v := r.URL.Query().Get("enabled"); v != "" {
		b := v == "true"
		enabled = &b
	}
	tag := r.URL.Query().Get("tag")
	limit := atoiDef(r.URL.Query().Get("limit"), 50)
	offset := atoiDef(r.URL.Query().Get("offset"), 0)
	jobs, total, err := a.store.ListJobs(r.Context(), enabled, tag, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	for i := range jobs {
		a.store.MaskJobSecrets(&jobs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "total": total, "limit": limit, "offset": offset})
}

func (a *API) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.Job
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	j := in.Job
	j.ID = uuid.Nil
	j.Enabled = in.Enabled == nil || *in.Enabled
	if j.Timezone == "" {
		j.Timezone = a.defaultTimezone(r)
	}
	if err := j.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.requireKnownCalendar(w, r, j.CalendarID) {
		return
	}
	if err := scheduler.ValidateJob(&j); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	next, ok, err := scheduler.InitialNextRun(&j, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var nextPtr *time.Time
	if ok {
		nextPtr = &next
	}
	created, err := a.store.CreateJob(r.Context(), &j, nextPtr)
	if err != nil {
		slog.Error("create job", "err", err)
		writeErr(w, http.StatusInternalServerError, "failed to create job")
		return
	}
	a.store.MaskJobSecrets(&created)
	a.audit(r, "job.create", "job", created.ID.String(), created.Name)
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	job, err := a.store.GetJob(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load job")
		return
	}
	a.store.MaskJobSecrets(&job)
	writeJSON(w, http.StatusOK, job)
}

func (a *API) handleUpdateJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in struct {
		store.Job
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	j := in.Job
	j.ID = id
	if in.Enabled != nil {
		j.Enabled = *in.Enabled
	} else {
		j.Enabled = true
	}
	if j.Timezone == "" {
		j.Timezone = a.defaultTimezone(r)
	}
	if err := j.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.requireKnownCalendar(w, r, j.CalendarID) {
		return
	}
	if err := scheduler.ValidateJob(&j); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	next, ok, err := scheduler.InitialNextRun(&j, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var nextPtr *time.Time
	if ok {
		nextPtr = &next
	}
	saved, err := a.store.UpdateJob(r.Context(), &j)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "job not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update job")
		return
	}
	if err := a.store.SetJobNextRun(r.Context(), id, nextPtr); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update schedule state")
		return
	}
	saved.NextRunAt = nextPtr
	a.store.MaskJobSecrets(&saved)
	a.audit(r, "job.update", "job", saved.ID.String(), saved.Name)
	writeJSON(w, http.StatusOK, saved)
}

func (a *API) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteJob(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete job")
		return
	}
	a.audit(r, "job.delete", "job", id.String(), "")
	w.WriteHeader(http.StatusNoContent)
}

// handleRunJob triggers an immediate manual run.
func (a *API) handleRunJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	job, err := a.store.GetJob(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load job")
		return
	}
	run, err := a.store.EnqueueWithPolicy(r.Context(), job, store.TriggerManual, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to enqueue run")
		return
	}
	if a.hub != nil {
		a.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
			"id":          run.ID,
			"job_id":      job.ID,
			"job_name":    job.Name,
			"status":      run.Status,
			"target_type": run.TargetType,
			"trigger":     store.TriggerManual,
		})
	}
	a.audit(r, "job.run", "job", job.ID.String(), job.Name)
	writeJSON(w, http.StatusAccepted, run)
}

func (a *API) requireKnownCalendar(w http.ResponseWriter, r *http.Request, id *uuid.UUID) bool {
	if id == nil || *id == uuid.Nil {
		return true
	}
	if _, err := a.store.GetCalendar(r.Context(), *id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "calendar not found")
			return false
		}
		writeErr(w, http.StatusInternalServerError, "failed to load calendar")
		return false
	}
	return true
}

// defaultTimezone resolves the instance default per request context.
func (a *API) defaultTimezone(r *http.Request) string {
	cfg, err := a.store.GetSettings(r.Context())
	if err != nil || cfg.DefaultTimezone == "" {
		return "UTC"
	}
	return cfg.DefaultTimezone
}

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}
