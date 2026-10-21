package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/store"
)

func adminIDFrom(r *http.Request) (uuid.UUID, bool) {
	id, ok := r.Context().Value(ctxAdminIDKey).(uuid.UUID)
	return id, ok
}

func (a *API) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	adminID, ok := adminIDFrom(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no session")
		return
	}
	toks, err := a.store.ListAPITokens(r.Context(), adminID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	if toks == nil {
		toks = []store.APIToken{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": toks})
}

func (a *API) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	ident := identityFrom(r)
	if ident.ID == uuid.Nil {
		writeErr(w, http.StatusUnauthorized, "no session")
		return
	}
	// A token is only ever minted from a signed-in session, and never
	// reaches past the role the caller holds right now.
	if ident.ViaToken {
		writeErr(w, http.StatusForbidden, "API tokens cannot create tokens; sign in to create one")
		return
	}
	var in struct {
		Name          string  `json:"name"`
		RoleCeiling   *string `json:"role_ceiling"`
		ExpiresInDays *int    `json:"expires_in_days"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	ceiling := ident.Role
	if in.RoleCeiling != nil && *in.RoleCeiling != "" {
		norm, err := store.NormalizeRole(*in.RoleCeiling)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		ceiling = store.MinRole(ident.Role, norm)
	}
	var expiresAt *time.Time
	if in.ExpiresInDays != nil {
		if *in.ExpiresInDays < 1 || *in.ExpiresInDays > 3650 {
			writeErr(w, http.StatusBadRequest, "expires_in_days must be between 1 and 3650")
			return
		}
		t := time.Now().Add(time.Duration(*in.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}
	tok, err := a.store.CreateAPIToken(r.Context(), ident.ID, in.Name, &ceiling, expiresAt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "token.create", "token", tok.ID.String(), tok.Name)
	writeJSON(w, http.StatusCreated, tok)
}

func (a *API) handleDeleteAPIToken(w http.ResponseWriter, r *http.Request) {
	adminID, ok := adminIDFrom(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no session")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteAPIToken(r.Context(), adminID, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete token")
		return
	}
	a.audit(r, "token.delete", "token", id.String(), "")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	limit := atoiDef(r.URL.Query().Get("limit"), 50)
	offset := atoiDef(r.URL.Query().Get("offset"), 0)
	events, err := a.store.ListAudit(r.Context(), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	if events == nil {
		events = []store.AuditEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
