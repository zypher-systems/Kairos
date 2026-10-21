package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/zypher-systems/kairos/internal/jobenv"
	"github.com/zypher-systems/kairos/internal/logutil"
	"github.com/zypher-systems/kairos/internal/protocol"
)

// DetectCapabilities reports what this agent can run.
func DetectCapabilities() map[string]string {
	caps := map[string]string{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	}
	if _, err := exec.LookPath("docker"); err == nil {
		caps["docker"] = "true"
	} else {
		caps["docker"] = "false"
	}
	caps["shell"] = "true"
	return caps
}

// Runner executes claimed RunSpecs locally and emits ordered output chunks.
type Runner struct {
	logs   chan protocol.LogChunk
	seq    atomic.Int64
	client *Client
	redact *logutil.Redactor
	runID  string
}

// NewRunner builds a runner bound to a per-run log channel.
func NewRunner(logQueue chan protocol.LogChunk, client *Client) *Runner {
	return &Runner{logs: logQueue, client: client}
}

// RedactSecrets masks the spec's secret values in everything emitted.
func (r *Runner) RedactSecrets(spec *protocol.RunSpec) {
	vals := make([]string, 0, len(spec.SecretKeys))
	for _, k := range spec.SecretKeys {
		if v, ok := spec.Env[k]; ok {
			vals = append(vals, v)
		}
	}
	r.redact = logutil.NewRedactor(vals)
}

// Emit queues a chunk with an assigned sequence.
func (r *Runner) Emit(stream, data string) {
	seq := r.seq.Add(1)
	r.logs <- protocol.LogChunk{Seq: seq, Stream: stream, Data: r.redact.Apply(data)}
}

// Execute runs one spec to completion. Returns status/exit/error.
func (r *Runner) Execute(ctx context.Context, spec *protocol.RunSpec) (status string, exitCode *int, errMsg string) {
	if spec.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutSec)*time.Second)
		defer cancel()
	}

	// Transfer specs run against storage backends, not a subprocess.
	if spec.ExecMode == "transfer" && spec.Transfer != nil {
		r.runID = spec.RunID.String()
		return r.executeTransfer(ctx, spec)
	}

	// The job sees a small allow-listed slice of the agent's environment
	// plus its own variables. KAIROS_AGENT_TOKEN in particular never
	// reaches a job: with it a job could claim other jobs' runs and secrets.
	env := jobenv.Merge(jobenv.Base(os.Environ()), spec.Env)

	var cmd *exec.Cmd
	switch spec.ExecMode {
	case "shell":
		cmd = exec.Command("/bin/sh", "-c", spec.ShellScript)
	case "docker":
		name := "kairos-" + dockerName(spec.RunID.String())
		args := []string{"run", "--rm", "--name", name}
		// "-e NAME" without a value makes docker read it from our
		// environment, so secrets stay out of argv and `ps`.
		for k := range spec.Env {
			args = append(args, "-e", k)
		}
		if spec.Workdir != "" {
			args = append(args, "-w", spec.Workdir)
		}
		args = append(args, spec.DockerImage)
		args = append(args, spec.DockerArgs...)
		cmd = exec.Command("docker", args...)
		// If we're killed (server cancel), stop the container too.
		stop := context.AfterFunc(ctx, func() {
			exec.Command("docker", "stop", "-t", "2", name).Run()
		})
		defer stop()
	default:
		c := -1
		return "failed", &c, fmt.Sprintf("unsupported exec_mode %q", spec.ExecMode)
	}

	// Isolate the command in its own process group; kill the group on
	// timeout/cancel so orphaned children release the output pipes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stopKill := context.AfterFunc(ctx, func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	defer stopKill()

	cmd.Env = env
	if spec.Workdir != "" && spec.ExecMode == "shell" {
		cmd.Dir = spec.Workdir
	}
	stdout := &runnerWriter{r: r, stream: "stdout"}
	stderr := &runnerWriter{r: r, stream: "stderr"}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	stdout.Flush()
	stderr.Flush()
	if err == nil {
		c := 0
		return "succeeded", &c, ""
	}
	if ctx.Err() != nil {
		c := -1
		return "failed", &c, "run canceled or timed out"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		c := ee.ExitCode()
		return "failed", &c, fmt.Sprintf("exit code %d", ee.ExitCode())
	}
	c := -1
	return "failed", &c, err.Error()
}

// runnerWriter turns process output into bounded chunks without splitting
// a multibyte character across two of them.
type runnerWriter struct {
	r      *Runner
	stream string
	split  logutil.Splitter
}

func (w *runnerWriter) Write(p []byte) (int, error) {
	for _, c := range w.split.Push(p) {
		w.r.Emit(w.stream, c)
	}
	return len(p), nil
}

// Flush emits bytes held back while waiting for the rest of a rune.
func (w *runnerWriter) Flush() {
	if rest := w.split.Flush(); rest != "" {
		w.r.Emit(w.stream, rest)
	}
}

// dockerName sanitizes a uuid into a docker container name suffix.
func dockerName(id string) string {
	var b strings.Builder
	for _, ch := range id {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '-', ch == '_', ch == '.':
			b.WriteRune(ch)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 40 {
		out = out[:40]
	}
	if out == "" {
		return "run"
	}
	return out
}
