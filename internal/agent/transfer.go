package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/zypher-systems/kairos/internal/protocol"
	"github.com/zypher-systems/kairos/internal/storage"
)

// executeTransfer performs a file move on the agent. Each side is a
// storage backend the agent opens itself (its filesystem, an SFTP host, an
// S3 bucket) or the server's library over the agent API. Bytes never pass
// through the Kairos server unless the library is one end.
func (r *Runner) executeTransfer(ctx context.Context, spec *protocol.RunSpec) (status string, exitCode *int, errMsg string) {
	t := spec.Transfer
	fail := func(format string, args ...any) (string, *int, string) {
		c := -1
		return "failed", &c, fmt.Sprintf(format, args...)
	}
	if t.Source.Storage == nil && !t.Source.Library || t.Dest.Storage == nil && !t.Dest.Library {
		// A pre-1.16 server sends only the legacy fields.
		return r.executeLegacyTransfer(ctx, spec)
	}

	srcB, err := r.openSide(ctx, t.Source)
	if err != nil {
		return fail("open source (%s): %v", t.Source.Name, err)
	}
	defer srcB.Close()
	dstB, err := r.openSide(ctx, t.Dest)
	if err != nil {
		return fail("open destination (%s): %v", t.Dest.Name, err)
	}
	defer dstB.Close()

	var files []string
	srcPattern := t.Source.Path
	switch {
	case len(t.Files) > 0:
		files = t.Files
		srcPattern = "*"
		r.Emit("system", fmt.Sprintf("source: %d file(s) from the trigger on %s", len(files), t.Source.Name))
	case t.Source.Library:
		files = []string{t.Source.FileID}
		srcPattern = t.Source.FileID
		if t.Dest.Path == "" || strings.HasSuffix(t.Dest.Path, "/") {
			t.Dest.Path = strings.TrimRight(t.Dest.Path, "/") + "/" + t.Source.FileName
		}
		r.Emit("system", "source: library/"+t.Source.FileName)
	case storage.HasGlob(t.Source.Path):
		entries, err := srcB.List(ctx, t.Source.Path)
		if err != nil {
			return fail("list %s: %v", t.Source.Path, err)
		}
		for _, e := range entries {
			files = append(files, e.Path)
		}
		if len(files) == 0 {
			return fail("no files match %s on %s", t.Source.Path, t.Source.Name)
		}
		r.Emit("system", fmt.Sprintf("source: %s on %s (%d files)", t.Source.Path, t.Source.Name, len(files)))
	default:
		files = []string{t.Source.Path}
		r.Emit("system", fmt.Sprintf("source: %s on %s", t.Source.Path, t.Source.Name))
	}
	r.Emit("system", fmt.Sprintf("destination: %s on %s", t.Dest.Path, t.Dest.Name))

	opt := storage.CopyOptions{
		Overwrite:     t.Overwrite,
		PreserveMTime: t.PreserveMTime,
		Verify:        t.VerifyChecksum && !t.Dest.Library,
		Progress:      func(n int64) { r.Emit("system", "…"+storage.HumanBytes(n)) },
	}
	var total int64
	for _, f := range files {
		dstPath := storage.DestPath(srcPattern, f, t.Dest.Path)
		if t.Dest.Library {
			name := path.Base(f)
			if len(files) == 1 && t.Dest.FileName != "" {
				name = t.Dest.FileName
			}
			dstPath = name
		}
		res, err := storage.CopyFile(ctx, srcB, f, dstB, dstPath, opt)
		if err != nil {
			return fail("%s: %v (after %s)", path.Base(f), err, storage.HumanBytes(res.Bytes))
		}
		total += res.Bytes
		shown := f
		if t.Source.Library {
			shown = t.Source.FileName
		}
		r.Emit("system", fmt.Sprintf("copied %s → %s (%s, sha256 %s…)", shown, dstPath, storage.HumanBytes(res.Bytes), res.SHA256[:12]))
		if t.DeleteSource && !t.Source.Library {
			if err := srcB.Remove(ctx, f); err != nil {
				return fail("delete source %s: %v", f, err)
			}
			r.Emit("system", "source removed: "+shown)
		}
	}
	r.Emit("system", fmt.Sprintf("done — %d file(s), %s transferred", len(files), storage.HumanBytes(total)))
	c := 0
	return "succeeded", &c, ""
}

func (r *Runner) openSide(ctx context.Context, side protocol.TransferSide) (storage.Backend, error) {
	if side.Library {
		return &libraryBackend{client: r.client, runID: r.runID}, nil
	}
	return storage.Open(ctx, *side.Storage)
}

// executeLegacyTransfer handles the pre-1.16 direction/path form.
func (r *Runner) executeLegacyTransfer(ctx context.Context, spec *protocol.RunSpec) (status string, exitCode *int, errMsg string) {
	t := spec.Transfer
	fail := func(msg string) (string, *int, string) {
		c := -1
		return "failed", &c, msg
	}
	local, _ := storage.NewLocal("")
	lib := &libraryBackend{client: r.client, runID: r.runID}
	switch t.Direction {
	case "push":
		r.Emit("system", fmt.Sprintf("downloading library file %s → %s", t.FileID, t.RemotePath))
		if _, err := storage.CopyFile(ctx, lib, t.FileID, local, t.RemotePath, storage.CopyOptions{Overwrite: t.Overwrite}); err != nil {
			return fail(fmt.Sprintf("download: %v", err))
		}
	case "pull":
		r.Emit("system", fmt.Sprintf("uploading %s → library/%s", t.RemotePath, t.FileName))
		if _, err := storage.CopyFile(ctx, local, t.RemotePath, lib, t.FileName, storage.CopyOptions{Overwrite: true}); err != nil {
			return fail(fmt.Sprintf("upload: %v", err))
		}
		if t.DeleteSource {
			if err := local.Remove(ctx, t.RemotePath); err != nil {
				return fail(fmt.Sprintf("delete source: %v", err))
			}
			r.Emit("system", "source removed")
		}
	default:
		return fail(fmt.Sprintf("unknown direction %q", t.Direction))
	}
	r.Emit("system", "transfer complete")
	c := 0
	return "succeeded", &c, ""
}

// libraryBackend is the server's file library seen through the agent API.
// Open downloads by file id; Create uploads under a name, registering the
// file when the upload completes.
type libraryBackend struct {
	client *Client
	runID  string
}

func (l *libraryBackend) Kind() storage.Kind { return "library" }

func (l *libraryBackend) List(ctx context.Context, pattern string) ([]storage.Entry, error) {
	return nil, fmt.Errorf("the library cannot be listed from an agent")
}

func (l *libraryBackend) Stat(ctx context.Context, p string) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}

func (l *libraryBackend) Open(ctx context.Context, fileID string) (io.ReadCloser, storage.Entry, error) {
	resp, err := l.client.do(ctx, "GET", "/api/v1/agent/files/"+fileID+"/content", nil)
	if err != nil {
		return nil, storage.Entry{}, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, storage.Entry{}, statusErr("download", resp)
	}
	return resp.Body, storage.Entry{Path: fileID, Size: resp.ContentLength, ModTime: time.Now()}, nil
}

func (l *libraryBackend) Create(ctx context.Context, name string, overwrite bool) (storage.Writer, error) {
	pr, pw := io.Pipe()
	w := &libraryWriter{pw: pw, done: make(chan error, 1)}
	go func() {
		w.done <- l.client.UploadStream(ctx, l.runID, pr, name)
	}()
	return w, nil
}

type libraryWriter struct {
	pw   *io.PipeWriter
	done chan error
	err  error
	once bool
}

func (w *libraryWriter) Write(p []byte) (int, error) { return w.pw.Write(p) }

func (w *libraryWriter) Commit(time.Time) error {
	if w.once {
		return w.err
	}
	w.once = true
	w.pw.Close()
	w.err = <-w.done
	return w.err
}

func (w *libraryWriter) Abort() error {
	if w.once {
		return nil
	}
	w.once = true
	w.pw.CloseWithError(fmt.Errorf("aborted"))
	<-w.done
	return nil
}

func (l *libraryBackend) Remove(ctx context.Context, p string) error {
	return fmt.Errorf("library files are removed on the server")
}

func (l *libraryBackend) Close() error { return nil }
