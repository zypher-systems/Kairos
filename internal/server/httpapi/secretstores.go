package httpapi

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// GET /api/v1/secret-stores
func (a *API) handleListSecretStores(w http.ResponseWriter, r *http.Request) {
	stores, err := a.store.ListSecretStores(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list secret stores")
		return
	}
	if stores == nil {
		stores = []store.SecretStore{}
	}
	for i := range stores {
		store.MaskSecretStore(&stores[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"stores": stores})
}

// POST /api/v1/secret-stores
func (a *API) handleCreateSecretStore(w http.ResponseWriter, r *http.Request) {
	var in store.SecretStore
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := a.store.CreateSecretStore(r.Context(), &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	store.MaskSecretStore(&in)
	a.audit(r, "secret_store.create", "secret_store", in.ID.String(), in.Name)
	writeJSON(w, http.StatusCreated, in)
}

// PUT /api/v1/secret-stores/{id}
func (a *API) handleUpdateSecretStore(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in store.SecretStore
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	in.ID = id
	if err := a.store.UpdateSecretStore(r.Context(), &in); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "secret store not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	store.MaskSecretStore(&in)
	a.audit(r, "secret_store.update", "secret_store", in.ID.String(), in.Name)
	writeJSON(w, http.StatusOK, in)
}

// DELETE /api/v1/secret-stores/{id}
func (a *API) handleDeleteSecretStore(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteSecretStore(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete secret store")
		return
	}
	a.audit(r, "secret_store.delete", "secret_store", id.String(), "")
	w.WriteHeader(http.StatusNoContent)
}
