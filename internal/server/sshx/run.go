package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// OutputSink receives ordered-ish streamed output lines.
type OutputSink interface {
	WriteChunk(stream string, data string)
}

// WriteSink adapts a func to the OutputSink interface.
type WriteSinkFunc func(stream string, data string)

func (f WriteSinkFunc) WriteChunk(stream string, data string) { f(stream, data) }

// Command describes a remote command and its environment.
type Command struct {
	Script  string // for shell mode: executed via /bin/sh -s (stdin fed)
	Env     map[string]string
	Workdir string
	Timeout time.Duration
}

// Run executes a command over an established ssh session and streams output.
// Returns the exit code (127 when the remote shell is missing).
func Run(ctx context.Context, client *ssh.Client, cmd Command, sink OutputSink) (int, error) {
	session, err := client.NewSession()
	if err != nil {
		return -1, fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	if cmd.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	var env []string
	for k, v := range cmd.Env {
		env = append(env, k+"="+v)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		return -1, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return -1, err
	}

	// Prepare preamble: workdir + env.
	var buf bytes.Buffer
	if len(env) > 0 {
		for _, e := range env {
			fmt.Fprintf(&buf, "export %s\n", shellQuote(e))
		}
	}
	if cmd.Workdir != "" {
		fmt.Fprintf(&buf, "cd %s 2>/dev/null || cd %s\n", shellQuote(cmd.Workdir), shellQuote(cmd.Workdir))
	}

	if err := session.Start("/bin/sh -s"); err != nil {
		return -1, err
	}

	// Managed kill on context cancel.
	go func() {
		<-ctx.Done()
		session.Signal(ssh.SIGKILL)
		session.Close()
	}()

	// Feed stdin (preamble + script), then close.
	go func() {
		defer stdin.Close()
		buf.WriteString(cmd.Script)
		if _, err := io.WriteString(stdin, buf.String()); err != nil {
			return
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go copyChunks(stdout, "stdout", sink, &wg)
	go copyChunks(stderr, "stderr", sink, &wg)

	sendErr := session.Wait()
	wg.Wait()

	if ctx.Err() != nil {
		return -1, fmt.Errorf("ssh command timeout/canceled: %w", ctx.Err())
	}

	exitCode := 0
	if sendErr != nil {
		var ee *ssh.ExitError
		if errors.As(sendErr, &ee) {
			exitCode = ee.ExitStatus()
		} else {
			return -1, sendErr
		}
	}
	return exitCode, nil
}

func copyChunks(r io.Reader, stream string, sink OutputSink, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			sink.WriteChunk(stream, string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

// shellQuote safely quotes for POSIX sh.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '"', '\'', '"', '\'')
		} else {
			out = append(out, s[i])
		}
	}
	out = append(out, '\'')
	return string(out)
}

// TestConnection dials and reports the verified fingerprint; used by the
// "test connection" flow in the UI.
func TestConnection(ctx context.Context, cfg HostConfig) (string, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 8 * time.Second
	}
	client, res, err := Dial(cfg)
	if err != nil {
		return "", err
	}
	defer client.Close()
	s, err := client.NewSession()
	if err != nil {
		return res.Fingerprint, err
	}
	defer s.Close()
	if err := s.Run("echo ok"); err != nil {
		return res.Fingerprint, err
	}
	return res.Fingerprint, nil
}
