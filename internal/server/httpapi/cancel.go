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

// POST /api/v1/runs/{id}/cancel — admin cancellation; executors observe via
// status guard, lease drop, and heartbeat kill instructions.
func (a *API) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id")
		return
	}
	run, err := a.store.GetRun(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load run")
		return
	}
	if err := a.store.CancelRuns(r.Context(), []uuid.UUID{id}); err != nil {
		writeErr(w, http.StatusInternalServerError, "cancel failed")
		return
	}
	if a.hub != nil {
		a.hub.Publish(ws.TopicActivity, "run_finished", map[string]any{
			"id":          run.ID,
			"job_id":      run.JobID,
			"status":      store.RunCanceled,
			"target_type": run.TargetType,
		})
	}
	a.audit(r, "run.cancel", "run", id.String(), "")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": store.RunCanceled})
}
