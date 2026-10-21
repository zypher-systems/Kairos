package httpapi

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// GET /api/v1/calendars
func (a *API) handleListCalendars(w http.ResponseWriter, r *http.Request) {
	cals, err := a.store.ListCalendars(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list calendars")
		return
	}
	if cals == nil {
		cals = []store.Calendar{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"calendars": cals})
}

// POST /api/v1/calendars
func (a *API) handleCreateCalendar(w http.ResponseWriter, r *http.Request) {
	var in store.Calendar
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := a.store.CreateCalendar(r.Context(), &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "calendar.create", "calendar", in.ID.String(), in.Name)
	writeJSON(w, http.StatusCreated, in)
}

// PUT /api/v1/calendars/{id}
func (a *API) handleUpdateCalendar(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in store.Calendar
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	in.ID = id
	if err := a.store.UpdateCalendar(r.Context(), &in); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "calendar not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "calendar.update", "calendar", in.ID.String(), in.Name)
	writeJSON(w, http.StatusOK, in)
}

// DELETE /api/v1/calendars/{id}
func (a *API) handleDeleteCalendar(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteCalendar(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete calendar")
		return
	}
	a.audit(r, "calendar.delete", "calendar", id.String(), "")
	w.WriteHeader(http.StatusNoContent)
}
