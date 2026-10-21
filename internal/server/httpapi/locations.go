package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/endpoints"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/storage"
)

// Storage locations: the places files live. Transfers and file triggers
// refer to them by id; credentials stay here, sealed.

// GET /api/v1/storage-locations
func (a *API) handleListStorageLocations(w http.ResponseWriter, r *http.Request) {
	locs, err := a.store.ListStorageLocations(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list storage locations")
		return
	}
	if locs == nil {
		locs = []store.StorageLocation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": locs})
}

// POST /api/v1/storage-locations
func (a *API) handleCreateStorageLocation(w http.ResponseWriter, r *http.Request) {
	var in store.StorageLocation
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := a.store.CreateStorageLocation(r.Context(), &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "storage_location.create", "storage_location", in.ID.String(), in.Name)
	writeJSON(w, http.StatusCreated, in)
}

// PUT /api/v1/storage-locations/{id}
func (a *API) handleUpdateStorageLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in store.StorageLocation
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	in.ID = id
	if err := a.store.UpdateStorageLocation(r.Context(), &in); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "storage location not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "storage_location.update", "storage_location", id.String(), in.Name)
	writeJSON(w, http.StatusOK, in)
}

// DELETE /api/v1/storage-locations/{id}
func (a *API) handleDeleteStorageLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	loc, err := a.store.GetStorageLocation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "storage location not found")
		return
	}
	if err := a.store.DeleteStorageLocation(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete storage location")
		return
	}
	a.audit(r, "storage_location.delete", "storage_location", id.String(), loc.Name)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/storage-locations/{id}/test — open the location and list
// its base path, so a typo in a bucket name or a mount that is not there
// shows up before a transfer depends on it. Agent locations cannot be
// tested from the server.
func (a *API) handleTestStorageLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	loc, err := a.store.GetStorageLocationForDelivery(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "storage location not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load storage location")
		return
	}
	if loc.Kind == "agent" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "agent locations are used by the agent; test them with a transfer job"})
		return
	}
	side, err := endpoints.FromLocation(r.Context(), a.store, loc)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	b, err := storage.Open(ctx, side.Spec)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer b.Close()
	entries, err := b.List(ctx, "*")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	names := make([]string, 0, len(entries))
	for i, e := range entries {
		if i >= 20 {
			break
		}
		names = append(names, e.Path)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": len(entries), "sample": names})
}
