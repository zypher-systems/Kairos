package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/store"
)

func (a *API) handleLogSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeErr(w, http.StatusBadRequest, "q must be at least 2 characters")
		return
	}
	var jobID *uuid.UUID
	if v := r.URL.Query().Get("job_id"); v != "" {
		id, err := parseUUID(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid job_id")
			return
		}
		jobID = &id
	}
	hits, err := a.store.SearchLogs(r.Context(), q, jobID, atoiDef(r.URL.Query().Get("limit"), 50), atoiDef(r.URL.Query().Get("offset"), 0))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to search logs")
		return
	}
	if hits == nil {
		hits = []store.LogSearchHit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "q": q})
}
