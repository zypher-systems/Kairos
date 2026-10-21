package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
)

// POST /api/v1/runs/{id}/approve — pending_approval → queued.
func (a *API) handleApproveRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id")
		return
	}
	run, err := a.store.ApproveRun(r.Context(), id, identityFrom(r).ID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if errors.Is(err, store.ErrNotPending) {
		writeErr(w, http.StatusConflict, "run is not pending approval")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "approve failed")
		return
	}
	if a.hub != nil {
		a.hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
			"id":          run.ID,
			"job_id":      run.JobID,
			"status":      run.Status,
			"target_type": run.TargetType,
			"trigger":     run.Trigger,
		})
	}
	a.audit(r, "run.approve", "run", id.String(), "")
	writeJSON(w, http.StatusOK, run)
}

// POST /api/v1/runs/{id}/reject — pending_approval → skipped.
func (a *API) handleRejectRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id")
		return
	}
	run, err := a.store.RejectRun(r.Context(), id, identityFrom(r).ID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if errors.Is(err, store.ErrNotPending) {
		writeErr(w, http.StatusConflict, "run is not pending approval")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reject failed")
		return
	}
	if a.hub != nil {
		a.hub.Publish(ws.TopicActivity, "run_finished", map[string]any{
			"id":          run.ID,
			"job_id":      run.JobID,
			"status":      run.Status,
			"target_type": run.TargetType,
		})
	}
	a.audit(r, "run.reject", "run", id.String(), "")
	writeJSON(w, http.StatusOK, run)
}
