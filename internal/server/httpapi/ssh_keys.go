package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/sshx"
	"github.com/zypher-systems/kairos/internal/server/store"
)

// GET /api/v1/ssh-keys
func (a *API) handleListSSHKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.store.ListSSHKeys(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list ssh keys")
		return
	}
	if keys == nil {
		keys = []store.SSHKey{}
	}
	// Annotate with host usage counts.
	type keyOut struct {
		store.SSHKey
		HostsUsing int `json:"hosts_using"`
	}
	out := make([]keyOut, 0, len(keys))
	for _, k := range keys {
		n, _ := a.store.CountHostsUsingKey(r.Context(), k.ID)
		out = append(out, keyOut{k, n})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// POST /api/v1/ssh-keys — generate a fresh keypair, or import one.
func (a *API) handleCreateSSHKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string `json:"name"`
		Generate   bool   `json:"generate"`    // server creates an ed25519 pair
		PrivateKey string `json:"private_key"` // PEM, when importing
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}

	k := store.SSHKey{Name: in.Name}
	var privPEM string
	if in.Generate {
		pem, _, _, err := store.GenerateSSHKeypair("kairos@" + in.Name)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to generate keypair")
			return
		}
		privPEM = pem
	} else {
		if in.PrivateKey == "" {
			writeErr(w, http.StatusBadRequest, "private_key required when not generating")
			return
		}
		privPEM = in.PrivateKey
	}

	if err := a.store.CreateSSHKey(r.Context(), &k, privPEM); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

// DELETE /api/v1/ssh-keys/{id}
func (a *API) handleDeleteSSHKey(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteSSHKey(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete ssh key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/ssh-keys/{id}/push — install the public key on a host via
// one-time password auth. Body: {"host_id": "...", "password": "..."}.
func (a *API) handlePushSSHKey(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in struct {
		HostID   string `json:"host_id"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if in.HostID == "" || in.Password == "" {
		writeErr(w, http.StatusBadRequest, "host_id and password are required")
		return
	}

	key, err := a.store.GetSSHKey(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "ssh key not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load key")
		return
	}
	hostID, err := uuid.Parse(in.HostID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid host_id")
		return
	}
	host, err := a.store.GetSSHHost(r.Context(), hostID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "ssh host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load host")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	learned, err := sshx.InstallPublicKey(ctx, host.Host, host.Port, host.Username, in.Password, key.PublicKey, host.HostKeyFingerprint)
	if err != nil {
		slog.Warn("key push failed", "host", host.Host, "port", host.Port, "user", host.Username, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": fmt.Sprintf("%v", err)})
		return
	}

	// Switch the host to the managed key now that the public half is installed.
	host.AuthType = "managed_key"
	host.SSHKeyID = &key.ID
	if host.HostKeyFingerprint == "" {
		host.HostKeyFingerprint = learned
	}
	if err := a.store.UpdateSSHHost(r.Context(), &host, "", ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "key pushed but host update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
