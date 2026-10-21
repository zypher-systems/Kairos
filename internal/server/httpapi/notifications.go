package httpapi

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// GET /api/v1/notifications
func (a *API) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := a.store.ListChannels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	if channels == nil {
		channels = []store.NotificationChannel{}
	}
	for i := range channels {
		store.MaskChannelSecrets(&channels[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

// POST /api/v1/notifications
func (a *API) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.NotificationChannel
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	c := in.NotificationChannel
	c.Enabled = in.Enabled == nil || *in.Enabled
	if err := a.store.CreateChannel(r.Context(), &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	store.MaskChannelSecrets(&c)
	writeJSON(w, http.StatusCreated, c)
}

// PUT /api/v1/notifications/{id}
func (a *API) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in struct {
		store.NotificationChannel
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	c := in.NotificationChannel
	c.ID = id
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if err := a.store.UpdateChannel(r.Context(), &c); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "channel not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := a.store.GetChannel(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to reload channel")
		return
	}
	store.MaskChannelSecrets(&saved)
	writeJSON(w, http.StatusOK, saved)
}

// DELETE /api/v1/notifications/{id}
func (a *API) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteChannel(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete channel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/notifications/{id}/test — send a synthetic event now.
func (a *API) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	ch, err := a.store.GetChannelForDelivery(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load channel")
		return
	}
	if a.notifier == nil {
		writeErr(w, http.StatusServiceUnavailable, "notifier unavailable")
		return
	}
	if err := a.notifier.Test(r.Context(), ch); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/v1/notifications/dead-letters — notifications that exhausted
// their delivery attempts and wait for an operator.
func (a *API) handleListDeadLetters(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.ListDeadOutbox(r.Context(), 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list dead letters")
		return
	}
	if rows == nil {
		rows = []store.NotifyOutboxRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dead_letters": rows})
}

// POST /api/v1/notifications/dead-letters/{id}/retry — queue it again now.
func (a *API) handleRetryDeadLetter(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	err := a.store.RetryDeadOutbox(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "dead letter not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "retry failed")
		return
	}
	a.audit(r, "notification.retry", "notification_delivery", id.String(), "")
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/notifications/dead-letters/{id} — dismiss it.
func (a *API) handleDeleteDeadLetter(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	err := a.store.DeleteDeadOutbox(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "dead letter not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "dismiss failed")
		return
	}
	a.audit(r, "notification.dismiss", "notification_delivery", id.String(), "")
	w.WriteHeader(http.StatusNoContent)
}
