package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/sshx"
	"github.com/zypher-systems/kairos/internal/server/store"
)

// ---------- agents ----------

// GET /api/v1/agents
func (a *API) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := a.store.ListAgents(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list agents")
		return
	}
	timeout := a.cfg.HeartbeatTimeout
	out := make([]map[string]any, 0, len(agents))
	for _, ag := range agents {
		online := ag.Status == "online" && ag.LastHeartbeatAt != nil &&
			time.Since(*ag.LastHeartbeatAt) < timeout
		out = append(out, map[string]any{
			"id":                ag.ID,
			"name":              ag.Name,
			"hostname":          ag.Hostname,
			"labels":            ag.Labels,
			"capabilities":      ag.Capabilities,
			"version":           ag.Version,
			"online":            online,
			"last_heartbeat_at": ag.LastHeartbeatAt,
			"created_at":        ag.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

// POST /api/v1/agents — issue a new agent identity; token returned once.
func (a *API) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	agent, token, err := a.store.CreateAgent(r.Context(), in.Name, in.Labels)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create agent")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"agent": agent,
		"token": token,
	})
}

// DELETE /api/v1/agents/{id}
func (a *API) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteAgent(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete agent")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/agents/{id}/labels
func (a *API) handleUpdateAgentLabels(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in struct {
		Labels map[string]string `json:"labels"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := a.store.UpdateAgentLabels(r.Context(), id, in.Labels); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update labels")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- ssh hosts ----------

// GET /api/v1/ssh-hosts
func (a *API) handleListSSHHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.store.ListSSHHosts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list ssh hosts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
}

// POST /api/v1/ssh-hosts
func (a *API) handleCreateSSHHost(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name               string     `json:"name"`
		Host               string     `json:"host"`
		Port               int        `json:"port"`
		Username           string     `json:"username"`
		AuthType           string     `json:"auth_type"`
		SSHKeyID           *uuid.UUID `json:"ssh_key_id"`
		PrivateKey         string     `json:"private_key"`
		Password           string     `json:"password"`
		HostKeyFingerprint string     `json:"host_key_fingerprint"`
		VerifyHostKey      bool       `json:"verify_host_key"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if in.AuthType == "" {
		in.AuthType = "key"
	}
	h := store.SSHHost{
		Name:               in.Name,
		Host:               in.Host,
		Port:               in.Port,
		Username:           in.Username,
		AuthType:           in.AuthType,
		SSHKeyID:           in.SSHKeyID,
		HostKeyFingerprint: in.HostKeyFingerprint,
		VerifyHostKey:      in.VerifyHostKey,
	}
	if err := a.store.CreateSSHHost(r.Context(), &h, in.PrivateKey, in.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

// PUT /api/v1/ssh-hosts/{id}
func (a *API) handleUpdateSSHHost(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name               string     `json:"name"`
		Host               string     `json:"host"`
		Port               int        `json:"port"`
		Username           string     `json:"username"`
		AuthType           string     `json:"auth_type"`
		SSHKeyID           *uuid.UUID `json:"ssh_key_id"`
		PrivateKey         string     `json:"private_key"`
		Password           string     `json:"password"`
		HostKeyFingerprint string     `json:"host_key_fingerprint"`
		VerifyHostKey      bool       `json:"verify_host_key"`
		ForgetHostKey      bool       `json:"forget_host_key"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	h := store.SSHHost{
		ID:                 id,
		ForgetHostKey:      in.ForgetHostKey,
		Name:               in.Name,
		Host:               in.Host,
		Port:               in.Port,
		Username:           in.Username,
		AuthType:           in.AuthType,
		SSHKeyID:           in.SSHKeyID,
		HostKeyFingerprint: in.HostKeyFingerprint,
		VerifyHostKey:      in.VerifyHostKey,
	}
	if err := a.store.UpdateSSHHost(r.Context(), &h, in.PrivateKey, in.Password); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "ssh host not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/ssh-hosts/{id}
func (a *API) handleDeleteSSHHost(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteSSHHost(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete ssh host")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/ssh-hosts/{id}/test — verify connectivity, return fingerprint.
func (a *API) handleTestSSHHost(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	key, password, fp, found, err := a.store.GetSSHCredentials(r.Context(), id)
	if err != nil || !found {
		writeErr(w, http.StatusNotFound, "ssh host not found")
		return
	}
	host, err := a.store.GetSSHHost(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "ssh host not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	learned, err := sshx.TestConnection(ctx, sshx.HostConfig{
		Host:               host.Host,
		Port:               host.Port,
		Username:           host.Username,
		PrivateKeyPEM:      key,
		Password:           password,
		HostKeyFingerprint: fp,
		VerifyHostKey:      host.VerifyHostKey,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if host.HostKeyFingerprint == "" && learned != "" {
		_ = a.store.SetSSHHostFingerprint(r.Context(), id, learned)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fingerprint": learned})
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
