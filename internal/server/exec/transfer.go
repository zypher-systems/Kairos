package exec

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/endpoints"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/storage"
)

// TransferRunner executes server-side file transfers between any two of:
// the managed library, a mount on the server, an SFTP host and an S3
// bucket. Bytes stream from source to destination; only library and mount
// destinations touch the server's disk. Transfers with an agent on either
// side are executed by that agent, not here.
type TransferRunner struct {
	store   *store.Store
	dataDir string
}

// NewTransferRunner builds the runner; dataDir is KAIROS_DATA_DIR.
func NewTransferRunner(st *store.Store, dataDir string) *TransferRunner {
	return &TransferRunner{store: st, dataDir: dataDir}
}

// Execute performs one transfer run, streaming progress to the sink.
func (r *TransferRunner) Execute(ctx context.Context, job store.Job, run store.Run, sink *LogSink) (exitCode int, runErr string) {
	cfg := job.TransferConfig

	src, err := endpoints.Resolve(ctx, r.store, cfg.Source)
	if err != nil {
		return -1, fmt.Sprintf("source: %v", err)
	}
	if cfg.FromTrigger {
		// The files the trigger matched, read from where it watched.
		src, err = endpoints.WatchSide(ctx, r.store, job)
		if err != nil {
			return -1, fmt.Sprintf("trigger source: %v", err)
		}
	}
	dst, err := endpoints.Resolve(ctx, r.store, cfg.Dest)
	if err != nil {
		return -1, fmt.Sprintf("destination: %v", err)
	}
	if src.AgentID != nil || dst.AgentID != nil {
		return -1, "this transfer involves an agent and must run on it; the job's target is wrong"
	}
	sink.Redact(append(src.Spec.Secrets(), dst.Spec.Secrets()...))

	srcB, err := r.open(ctx, src, sink)
	if err != nil {
		return -1, fmt.Sprintf("open source (%s): %v", src.Name, err)
	}
	defer srcB.Close()
	dstB, err := r.open(ctx, dst, sink)
	if err != nil {
		return -1, fmt.Sprintf("open destination (%s): %v", dst.Name, err)
	}
	defer dstB.Close()

	// What to copy.
	var files []string
	srcPattern := src.Path
	var libraryName string
	switch {
	case cfg.FromTrigger:
		files = run.TriggerFiles
		if len(files) == 0 {
			sink.System("the trigger matched no files; nothing to transfer")
			return 0, ""
		}
		srcPattern = "*" // every trigger file lands under the destination directory
		sink.System("source: %d file(s) from the trigger on %s", len(files), src.Name)
	case src.Library:
		if cfg.Source.FileID == nil {
			return -1, "library source has no file"
		}
		f, err := r.store.GetFile(ctx, *cfg.Source.FileID)
		if err != nil {
			return -1, "library file not found"
		}
		files = []string{strings.TrimPrefix(f.StoragePath, "files/")}
		srcPattern = files[0]
		libraryName = f.Name
		if dst.Path == "" || strings.HasSuffix(dst.Path, "/") {
			dst.Path = strings.TrimRight(dst.Path, "/") + "/" + f.Name
			dst.Path = strings.TrimPrefix(dst.Path, "/")
			if dst.Spec.Root == "" && !dst.Library {
				dst.Path = "/" + dst.Path
			}
		}
		sink.System("source: library/%s (%s)", f.Name, storage.HumanBytes(f.SizeBytes))
	case storage.HasGlob(src.Path):
		entries, err := srcB.List(ctx, src.Path)
		if err != nil {
			return -1, fmt.Sprintf("list %s: %v", src.Path, err)
		}
		for _, e := range entries {
			files = append(files, e.Path)
		}
		if len(files) == 0 {
			return -1, fmt.Sprintf("no files match %s on %s", src.Path, src.Name)
		}
		sink.System("source: %s on %s (%d files)", src.Path, src.Name, len(files))
	default:
		files = []string{src.Path}
		sink.System("source: %s on %s", src.Path, src.Name)
	}
	sink.System("destination: %s on %s", orDot(dst.Path), dst.Name)

	opt := storage.CopyOptions{
		Overwrite:     cfg.Overwrite,
		PreserveMTime: cfg.PreserveMTime,
		Verify:        cfg.VerifyChecksum,
		Progress:      func(n int64) { sink.System("…%s", storage.HumanBytes(n)) },
	}
	var total int64
	for _, f := range files {
		dstPath := storage.DestPath(srcPattern, f, dst.Path)
		if dst.Library {
			// Library blobs get a random storage name; the row keeps the
			// real name.
			dstPath = uuid.NewString()
		}
		res, err := storage.CopyFile(ctx, srcB, f, dstB, dstPath, opt)
		if err != nil {
			return -1, fmt.Sprintf("%s: %v (after %s)", path.Base(f), err, storage.HumanBytes(res.Bytes))
		}
		total += res.Bytes
		shown := f
		if libraryName != "" {
			shown = libraryName
		}
		if dst.Library {
			name := path.Base(shown)
			if len(files) == 1 && !cfg.FromTrigger && cfg.Dest.Path != "" && !strings.HasSuffix(cfg.Dest.Path, "/") {
				name = path.Base(cfg.Dest.Path)
			}
			entry := store.FileEntry{Name: name, SizeBytes: res.Bytes, SHA256: res.SHA256, StoragePath: filepath.Join("files", dstPath)}
			if err := r.store.CreateFile(context.WithoutCancel(ctx), &entry); err != nil {
				_ = dstB.Remove(ctx, dstPath)
				return -1, fmt.Sprintf("register library file: %v", err)
			}
			sink.System("registered library file %s (sha256 %s…)", entry.Name, entry.SHA256[:12])
		} else {
			sink.System("copied %s → %s (%s, sha256 %s…)", shown, dstPath, storage.HumanBytes(res.Bytes), res.SHA256[:12])
		}
		// The source goes only after this file is safely in place.
		if cfg.DeleteSource {
			if src.Library && cfg.Source.FileID != nil {
				if p, err := r.store.DeleteFile(ctx, *cfg.Source.FileID); err == nil {
					_ = os.Remove(filepath.Join(r.dataDir, p))
				}
			} else if err := srcB.Remove(ctx, f); err != nil {
				return -1, fmt.Sprintf("delete source %s: %v", f, err)
			}
			sink.System("source removed: %s", shown)
		}
	}
	sink.System("done — %d file(s), %s transferred", len(files), storage.HumanBytes(total))
	return 0, ""
}

// open opens a resolved side. The library is a local backend rooted at the
// data directory's files/ folder.
func (r *TransferRunner) open(ctx context.Context, side endpoints.Resolved, sink *LogSink) (storage.Backend, error) {
	if side.Library {
		dir := filepath.Join(r.dataDir, "files")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		return storage.NewLocal(dir)
	}
	b, err := storage.Open(ctx, side.Spec)
	if err != nil {
		return nil, err
	}
	if s, ok := b.(*storage.SFTP); ok {
		sink.System("connected: %s@%s:%d", side.Spec.Username, side.Spec.Host, side.Spec.Port)
		if s.LearnedFingerprint != "" && side.SSHHostID != nil {
			sink.System("host key fingerprint: %s", s.LearnedFingerprint)
			if err := r.store.SetSSHHostFingerprint(ctx, *side.SSHHostID, s.LearnedFingerprint); err != nil {
				slog.Warn("persist fingerprint", "err", err)
			}
		}
	}
	return b, nil
}

func orDot(p string) string {
	if p == "" {
		return "."
	}
	return p
}
