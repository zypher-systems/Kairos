package httpapi

import (
	"errors"
	"github.com/google/uuid"
	"github.com/zypher-systems/kairos/internal/server/scheduler"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/workflow"
)

// GET /api/v1/workflows
func (a *API) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	wfs, err := a.store.ListWorkflows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}
	if wfs == nil {
		wfs = []store.Workflow{}
	}
	type wfOut struct {
		store.Workflow
		JobCount int `json:"job_count"`
	}
	out := make([]wfOut, 0, len(wfs))
	for _, wf := range wfs {
		jobs, _ := a.store.JobsInWorkflow(r.Context(), wf.ID)
		out = append(out, wfOut{wf, len(jobs)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": out})
}

// POST /api/v1/workflows
func (a *API) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var in workflowInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	wf := store.Workflow{Enabled: true}
	in.applyTo(&wf)
	if err := a.prepareWorkflowSchedule(&wf); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.CreateWorkflow(r.Context(), &wf); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "workflow.create", "workflow", wf.ID.String(), wf.Name)
	writeJSON(w, http.StatusCreated, wf)
}

// workflowInput is the writable part of a workflow. Omitted fields keep
// their stored values on update.
type workflowInput struct {
	Name            *string            `json:"name"`
	Description     *string            `json:"description"`
	Enabled         *bool              `json:"enabled"`
	Parameters      *map[string]string `json:"parameters"`
	ScheduleType    *string            `json:"schedule_type"`
	CronExpr        *string            `json:"cron_expr"`
	IntervalSeconds *int               `json:"interval_seconds"`
	OneoffAt        *time.Time         `json:"oneoff_at"`
	Timezone        *string            `json:"timezone"`
	CalendarID      *uuid.UUID         `json:"calendar_id"`
	WatchEnabled    *bool              `json:"watch_enabled"`
	WatchLocationID *uuid.UUID         `json:"watch_location_id"`
	WatchPath       *string            `json:"watch_path"`
	WatchInterval   *int               `json:"watch_interval_seconds"`
}

func (in workflowInput) applyTo(wf *store.Workflow) {
	if in.Name != nil {
		wf.Name = *in.Name
	}
	if in.Description != nil {
		wf.Description = *in.Description
	}
	if in.Enabled != nil {
		wf.Enabled = *in.Enabled
	}
	if in.Parameters != nil {
		wf.Parameters = *in.Parameters
	}
	if in.ScheduleType != nil {
		wf.ScheduleType = *in.ScheduleType
	}
	if in.CronExpr != nil {
		wf.CronExpr = *in.CronExpr
	}
	if in.IntervalSeconds != nil {
		wf.IntervalSeconds = *in.IntervalSeconds
	}
	if in.OneoffAt != nil {
		wf.OneoffAt = in.OneoffAt
	}
	if in.Timezone != nil {
		wf.Timezone = *in.Timezone
	}
	if in.CalendarID != nil {
		if *in.CalendarID == uuid.Nil {
			wf.CalendarID = nil
		} else {
			wf.CalendarID = in.CalendarID
		}
	}
	if in.WatchEnabled != nil {
		wf.WatchEnabled = *in.WatchEnabled
	}
	if in.WatchLocationID != nil {
		if *in.WatchLocationID == uuid.Nil {
			wf.WatchLocationID = nil
		} else {
			wf.WatchLocationID = in.WatchLocationID
		}
	}
	if in.WatchPath != nil {
		wf.WatchPath = *in.WatchPath
	}
	if in.WatchInterval != nil {
		wf.WatchIntervalSec = *in.WatchInterval
	}
}

// prepareWorkflowSchedule validates the schedule and sets next_run_at.
func (a *API) prepareWorkflowSchedule(wf *store.Workflow) error {
	if wf.Timezone == "" {
		wf.Timezone = "UTC"
	}
	if wf.ScheduleType == "" {
		wf.ScheduleType = scheduler.TypeManual
	}
	if err := scheduler.ValidateSchedule(scheduler.WorkflowSchedule(wf)); err != nil {
		return err
	}
	sc := scheduler.WorkflowSchedule(wf)
	sc.NextRunAt = nil // a changed schedule starts from now
	next, ok, err := scheduler.InitialNextFor(sc, time.Now())
	if err != nil {
		return err
	}
	if ok {
		wf.NextRunAt = &next
	} else {
		wf.NextRunAt = nil
	}
	return nil
}

// GET /api/v1/workflows/{id}
func (a *API) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	detail, err := a.store.GetWorkflowDetail(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load workflow")
		return
	}
	for i := range detail.Jobs {
		a.store.MaskJobSecrets(&detail.Jobs[i])
	}
	writeJSON(w, http.StatusOK, detail)
}

// PUT /api/v1/workflows/{id}
func (a *API) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in workflowInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	wf, err := a.store.GetWorkflow(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load workflow")
		return
	}
	before := wf
	in.applyTo(&wf)
	scheduleChanged := before.ScheduleType != wf.ScheduleType || before.CronExpr != wf.CronExpr ||
		before.IntervalSeconds != wf.IntervalSeconds || before.Timezone != wf.Timezone ||
		(before.OneoffAt == nil) != (wf.OneoffAt == nil) || (before.OneoffAt != nil && wf.OneoffAt != nil && !before.OneoffAt.Equal(*wf.OneoffAt)) ||
		before.Enabled != wf.Enabled
	if scheduleChanged {
		if err := a.prepareWorkflowSchedule(&wf); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if err := scheduler.ValidateSchedule(scheduler.WorkflowSchedule(&wf)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.UpdateWorkflow(r.Context(), &wf); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "workflow.update", "workflow", wf.ID.String(), wf.Name)
	writeJSON(w, http.StatusOK, wf)
}

// DELETE /api/v1/workflows/{id}
func (a *API) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteWorkflow(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete workflow")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/workflows/{id}/edges — replace the full edge set (editor save).
// Every job referenced by an edge is linked to this workflow.
func (a *API) handleReplaceWorkflowEdges(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in struct {
		Edges []store.WorkflowEdge `json:"edges"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := a.store.ReplaceWorkflowEdges(r.Context(), id, in.Edges); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.SetJobsWorkflowFromEdges(r.Context(), id, in.Edges); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to assign jobs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/workflows/{id}/run — manually fire the root jobs.
func (a *API) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	wf, err := a.store.GetWorkflow(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load workflow")
		return
	}
	if !wf.Enabled {
		writeErr(w, http.StatusConflict, "workflow is disabled")
		return
	}
	fired, err := workflow.RunWorkflow(r.Context(), a.store, a.hub, wf)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to run workflow")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"fired": fired})
}
