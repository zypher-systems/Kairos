package httpapi

import (
	"errors"
	"net/http"

	"github.com/zypher-systems/kairos/internal/server/store"
)

func (a *API) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	if users == nil {
		users = []store.Admin{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (a *API) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Team     string `json:"team"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	user, err := a.store.CreateUser(r.Context(), in.Username, in.Password, in.Role, in.Team)
	if err != nil {
		writeUserErr(w, err)
		return
	}
	a.audit(r, "user.create", "user", user.ID.String(), user.Username+" "+user.Role)
	writeJSON(w, http.StatusCreated, user)
}

func (a *API) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in struct {
		Role        string `json:"role"`
		Team        string `json:"team"`
		Enabled     *bool  `json:"enabled"`
		Password    string `json:"password"`
		OIDCSubject string `json:"oidc_subject"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	user, err := a.store.UpdateUser(r.Context(), id, in.Role, in.Team, enabled, in.Password, in.OIDCSubject)
	if err != nil {
		writeUserErr(w, err)
		return
	}
	a.audit(r, "user.update", "user", user.ID.String(), user.Username+" "+user.Role)
	writeJSON(w, http.StatusOK, user)
}

func (a *API) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteUser(r.Context(), id); err != nil {
		writeUserErr(w, err)
		return
	}
	a.audit(r, "user.delete", "user", id.String(), "")
	w.WriteHeader(http.StatusNoContent)
}

func writeUserErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrUsernameTaken):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrOIDCSubjectTaken):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrUserNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrLastAdmin):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeErr(w, http.StatusBadRequest, err.Error())
	}
}
