package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/store"
)

func (a *API) handleListRuns(w http.ResponseWriter, r *http.Request) {
	f := store.RunFilter{
		Status: r.URL.Query().Get("status"),
		Limit:  atoiDef(r.URL.Query().Get("limit"), 100),
		Offset: atoiDef(r.URL.Query().Get("offset"), 0),
	}
	if v := r.URL.Query().Get("job_id"); v != "" {
		id, err := parseUUID(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid job_id")
			return
		}
		f.JobID = &id
	}
	runs, err := a.store.ListRuns(r.Context(), f)
	if err != nil {
		slog.Error("list runs", "err", err)
		writeErr(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	total, err := a.store.CountRuns(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "total": total, "limit": f.Limit, "offset": f.Offset})
}

func (a *API) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
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
	writeJSON(w, http.StatusOK, run)
}

func (a *API) handleGetRunLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	after := int64(0)
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid after seq")
			return
		}
		after = n
	}
	chunks, err := a.store.Logs(r.Context(), id, after, atoiDef(r.URL.Query().Get("limit"), 200))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load logs")
		return
	}
	if chunks == nil {
		chunks = []store.LogChunk{}
	}
	next := after
	if len(chunks) > 0 {
		next = chunks[len(chunks)-1].Seq
	}
	writeJSON(w, http.StatusOK, map[string]any{"chunks": chunks, "next": next})
}

func atoiDef(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseUUID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}
