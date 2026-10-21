package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/zypher-systems/kairos/internal/jobenv"
	"github.com/zypher-systems/kairos/internal/logutil"
	"github.com/zypher-systems/kairos/internal/server/store"
)

// LocalRunner executes runs on the server host itself.
type LocalRunner struct {
	store *store.Store
}

// NewLocalRunner builds a local executor.
func NewLocalRunner(st *store.Store) *LocalRunner { return &LocalRunner{store: st} }

// Execute runs one local run to completion, streaming logs. Returns the exit
// code and error text for the run record. runName is used as the docker
// container name (sanitized).
func (r *LocalRunner) Execute(ctx context.Context, job store.Job, runName string, sink *LogSink) (exitCode int, runErr string) {
	env := jobenv.Merge(jobenv.Base(os.Environ()), job.MergedEnv())

	var cmd *exec.Cmd
	switch job.ExecMode {
	case "docker":
		args := []string{"run", "--rm", "--name", sanitize(runName)}
		// "-e NAME" without a value makes docker read it from our
		// environment (cmd.Env below), so secrets stay out of argv and `ps`.
		for k := range job.MergedEnv() {
			args = append(args, "-e", k)
		}
		if job.Workdir != "" {
			args = append(args, "-w", job.Workdir)
		}
		args = append(args, job.DockerImage)
		args = append(args, job.DockerCommand...)
		cmd = exec.Command("docker", args...)
		// If we're killed (timeout/cancel), stop the container too.
		stop := context.AfterFunc(ctx, func() {
			exec.Command("docker", "stop", "-t", "2", sanitize(runName)).Run()
		})
		defer stop()
	case "shell":
		cmd = exec.Command("/bin/sh", "-c", job.ShellScript)
		if job.Workdir != "" {
			cmd.Dir = job.Workdir
		}
	default:
		return -1, fmt.Sprintf("unsupported exec_mode %q", job.ExecMode)
	}
	cmd.Env = env

	// Kill the whole process group on timeout/cancel so orphaned children
	// can't hold the output pipes open.
	setProcessGroup(cmd)
	stopKill := context.AfterFunc(ctx, func() {
		if cmd.Process != nil {
			killGroup(cmd)
		}
	})
	defer stopKill()

	stdout := &chunkWriter{sink: sink, stream: "stdout"}
	stderr := &chunkWriter{sink: sink, stream: "stderr"}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	stdout.Flush()
	stderr.Flush()
	if err == nil {
		return 0, ""
	}
	if ctx.Err() != nil {
		return -1, "run canceled or timed out"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), fmt.Sprintf("exit code %d", ee.ExitCode())
	}
	return -1, err.Error()
}

// DockerAvailable reports whether docker exists on the server host.
func DockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// sanitize limits the docker container name to an acceptable set.
func sanitize(s string) string {
	var b strings.Builder
	for _, ch := range s {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '-', ch == '_', ch == '.':
			b.WriteRune(ch)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "kairos-run"
	}
	return out
}

// chunkWriter adapts a byte stream into LogSink chunks, never splitting a
// multibyte character across two chunks.
type chunkWriter struct {
	sink   *LogSink
	stream string
	split  logutil.Splitter
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	for _, c := range w.split.Push(p) {
		w.sink.WriteChunk(w.stream, c)
	}
	return len(p), nil
}

// Flush writes any bytes held back while waiting for the rest of a rune.
func (w *chunkWriter) Flush() {
	if rest := w.split.Flush(); rest != "" {
		w.sink.WriteChunk(w.stream, rest)
	}
}
