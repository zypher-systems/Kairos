package httpapi

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zypher-systems/kairos/internal/server/sshx"
	"github.com/zypher-systems/kairos/scripts"
)

// agentBinaryDir is where the server image ships the agent binaries.
const agentBinaryDir = "/usr/local/share/kairos/agent"

// handleAgentInstallScript serves the agent installer. Public: the script
// carries no secrets (the token is passed as a flag by the caller).
func (a *API) handleAgentInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-sh; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="kairos-agent-install.sh"`)
	_, _ = w.Write([]byte(scripts.AgentInstallScript))
}

// handleAgentDownload serves an agent binary. Public: binaries are not
// secret, and an install must work before any credentials exist.
func (a *API) handleAgentDownload(w http.ResponseWriter, r *http.Request) {
	os_ := chi.URLParam(r, "os")
	arch := chi.URLParam(r, "arch")
	if os_ != "linux" || (arch != "amd64" && arch != "arm64") {
		writeErr(w, http.StatusNotFound, "no such build")
		return
	}
	p := filepath.Join(agentBinaryDir, fmt.Sprintf("kairos-agent-linux-%s", arch))
	st, err := os.Stat(p)
	if err != nil || st.IsDir() || st.Size() == 0 {
		writeErr(w, http.StatusNotFound, "agent binary not available on this server")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kairos-agent-linux-%s"`, arch))
	http.ServeFile(w, r, p)
}

// handleAgentServerURL reports the URL agents will be configured to poll,
// so the UI can show (and warn about) it before an install.
func (a *API) handleAgentServerURL(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"server_url":  a.publicURL(r),
		"from_config": a.cfg.PublicURL != "",
	})
}

// handlePushInstallAgent installs an agent on an SSH host: creates an agent
// identity, connects with the host's stored credentials, and runs the
// installer remotely. The agent polls out to the server afterwards.
func (a *API) handlePushInstallAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name   string `json:"name"`
		Labels string `json:"labels"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}

	host, err := a.store.GetSSHHost(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "ssh host not found")
		return
	}
	key, password, fp, found, err := a.store.GetSSHCredentials(r.Context(), id)
	if err != nil || !found {
		writeErr(w, http.StatusNotFound, "ssh host not found")
		return
	}

	agentName := in.Name
	if agentName == "" {
		agentName = host.Name
	}
	labels := map[string]string{}
	for _, pair := range strings.Split(in.Labels, ",") {
		if kv := strings.SplitN(strings.TrimSpace(pair), "=", 2); len(kv) == 2 && kv[0] != "" {
			labels[kv[0]] = kv[1]
		}
	}

	agent, token, err := a.store.CreateAgent(r.Context(), agentName, labels)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create agent identity")
		return
	}

	serverURL := a.publicURL(r)
	// The installer is fed via stdin, so command-line flags must be baked
	// into the script itself: write a wrapper that invokes the installer
	// with its arguments.
	script := fmt.Sprintf(
		"tmp=$(mktemp)\ncat > \"$tmp\" <<'KAIROS_INSTALL_EOF'\n%s\nKAIROS_INSTALL_EOF\nsh \"$tmp\" %s %s %s %s %s %s %s %s\nrc=$?\nrm -f \"$tmp\"\nexit $rc\n",
		scripts.AgentInstallScript,
		shellQuote("--server"), shellQuote(serverURL),
		shellQuote("--token"), shellQuote(token),
		shellQuote("--name"), shellQuote(agentName),
		shellQuote("--labels"), shellQuote(in.Labels),
	)

	cfg := sshx.HostConfig{
		Host:               host.Host,
		Port:               host.Port,
		Username:           host.Username,
		PrivateKeyPEM:      key,
		Password:           password,
		HostKeyFingerprint: fp,
		VerifyHostKey:      host.VerifyHostKey,
		Timeout:            15 * time.Second,
	}
	client, res, err := sshx.Dial(cfg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": fmt.Sprintf("ssh dial: %v", err)})
		return
	}
	defer client.Close()
	if fp == "" && res.Fingerprint != "" {
		_ = a.store.SetSSHHostFingerprint(r.Context(), id, res.Fingerprint)
	}

	var out strings.Builder
	code, err := sshx.Run(r.Context(), client, sshx.Command{
		Script:  script,
		Timeout: 3 * time.Minute,
	}, sshx.WriteSinkFunc(func(stream, data string) {
		out.WriteString(data)
	}))
	if err != nil || code != 0 {
		slog.Warn("push-install failed", "host", host.Host, "code", code, "err", err, "output", out.String())
		msg := strings.TrimSpace(out.String())
		if msg == "" && err != nil {
			msg = err.Error()
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok": false, "error": fmt.Sprintf("install failed (exit %d): %s", code, lastLines(msg, 8)),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"agent":  agent,
		"output": lastLines(out.String(), 12),
	})
}

// publicURL derives the URL agents should use to reach this server:
// configured public URL, else the current request's scheme+host — with a
// loopback guard: if the request host is localhost/127.x (dev setups), the
// agent could never reach that from a remote machine, so we substitute the
// server's primary outbound IP instead.
func (a *API) publicURL(r *http.Request) string {
	if a.cfg.PublicURL != "" {
		return strings.TrimRight(a.cfg.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || a.cfg.HTTPS || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host, port := splitHostPort(r.Host)
	if isLoopbackHost(host) {
		if ip := outboundIP(); ip != "" {
			host = ip
		}
	}
	if port != "" {
		return scheme + "://" + net.JoinHostPort(host, port)
	}
	return scheme + "://" + host
}

func splitHostPort(h string) (string, string) {
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		return h, ""
	}
	return host, port
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// outboundIP discovers the server's primary outbound (LAN) IP by opening a
// UDP "connection" to a public address — no traffic is actually sent.
func outboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
