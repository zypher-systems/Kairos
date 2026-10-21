package exec

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zypher-systems/kairos/internal/logutil"
	"github.com/zypher-systems/kairos/internal/server/sshx"
	"github.com/zypher-systems/kairos/internal/server/store"
)

// SSHRunner pushes runs to remote hosts over SSH.
type SSHRunner struct {
	store *store.Store
}

// NewSSHRunner builds an SSH executor.
func NewSSHRunner(st *store.Store) *SSHRunner { return &SSHRunner{store: st} }

// Execute connects to the job's SSH host, streams output, and reports exit.
// Host-key TOFU: on first contact with no stored fingerprint, the learned
// fingerprint is persisted so subsequent connections verify it.
func (r *SSHRunner) Execute(ctx context.Context, job store.Job, runID string, sink *LogSink) (exitCode int, runErr string) {
	if job.SSHHostID == nil {
		return -1, "missing ssh_host_id"
	}
	key, password, fp, ok, err := r.store.GetSSHCredentials(ctx, *job.SSHHostID)
	if err != nil {
		return -1, fmt.Sprintf("decrypt host credentials: %v", err)
	}
	if !ok {
		return -1, "ssh host not found"
	}
	host, err := r.store.GetSSHHost(ctx, *job.SSHHostID)
	if err != nil {
		return -1, fmt.Sprintf("load ssh host: %v", err)
	}

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
		return -1, fmt.Sprintf("ssh dial %s@%s:%d: %v", host.Username, host.Host, host.Port, err)
	}
	defer client.Close()

	// TOFU: persist learned fingerprint on first contact.
	if fp == "" && res.Fingerprint != "" {
		sink.System("host key fingerprint: %s", res.Fingerprint)
		if err := r.store.SetSSHHostFingerprint(ctx, *job.SSHHostID, res.Fingerprint); err != nil {
			slog.Warn("persist fingerprint", "err", err)
		}
	}

	cmd := sshx.Command{
		Env:     job.MergedEnv(),
		Workdir: job.Workdir,
		Timeout: time.Duration(job.TimeoutSeconds) * time.Second,
	}

	switch job.ExecMode {
	case "shell":
		cmd.Script = job.ShellScript
	case "docker":
		cmd.Script = buildRemoteDockerScript(job, runID)
	default:
		return -1, fmt.Sprintf("unsupported exec_mode %q", job.ExecMode)
	}

	// One splitter per stream so a character cut by the SSH read buffer is
	// joined back before it is stored.
	splitters := map[string]*logutil.Splitter{"stdout": {}, "stderr": {}}
	code, err := sshx.Run(ctx, client, cmd, sshx.WriteSinkFunc(func(stream, data string) {
		sp := splitters[stream]
		if sp == nil {
			sink.WriteChunk(stream, data)
			return
		}
		for _, c := range sp.Push([]byte(data)) {
			sink.WriteChunk(stream, c)
		}
	}))
	for stream, sp := range splitters {
		if rest := sp.Flush(); rest != "" {
			sink.WriteChunk(stream, rest)
		}
	}
	if err != nil {
		return -1, fmt.Sprintf("remote execution failed: %v", err)
	}
	return code, ""
}

// buildRemoteDockerScript constructs the POSIX command run on the remote
// host. Variables are exported by the sshx preamble (fed on stdin), and
// docker picks them up by name, so no secret value lands in the remote
// process list.
func buildRemoteDockerScript(job store.Job, runID string) string {
	args := []string{"docker", "run", "--rm", "--name", sanitize(runID)}
	for k := range job.MergedEnv() {
		args = append(args, "-e", k)
	}
	if job.Workdir != "" {
		args = append(args, "-w", job.Workdir)
	}
	args = append(args, job.DockerImage)
	args = append(args, job.DockerCommand...)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quoteArg(a)
	}
	return fmt.Sprintf(`trap 'docker stop %s >/dev/null 2>&1 || true' EXIT INT TERM; %s`, quoteArg(sanitize(runID)), strings.Join(quoted, " "))
}

func quoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
