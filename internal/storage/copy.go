package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

// CopyOptions tune one file copy.
type CopyOptions struct {
	Overwrite     bool
	PreserveMTime bool
	// Verify re-reads the destination after commit and compares its
	// SHA-256 with the source's.
	Verify bool
	// Progress is called with the running byte count, at most about once a
	// second. May be nil.
	Progress func(n int64)
}

// CopyResult describes a finished copy.
type CopyResult struct {
	Bytes  int64
	SHA256 string
	Entry  Entry // the source entry
}

// identifier is a remote backend that can name a file independently of its
// root, so two locations over one share compare equal.
type identifier interface {
	identity(p string) (string, error)
}

// sameFile reports whether the two sides address one file. Paths on two
// different kinds of backend are never compared: a directory mounted on
// this machine and the NFS export behind it cannot be told apart from here.
//
// A match by name is final and is never weighed against a file's size or
// time: those change under a writer, and a wrong answer here costs the only
// copy. The one thing that can undo it is two SFTP accounts that do not see
// the same tree, which sameTree settles.
func sameFile(src Backend, srcPath string, dst Backend, dstPath string) bool {
	if a, ok := src.(*Local); ok {
		b, ok := dst.(*Local)
		return ok && a.sameFile(srcPath, b, dstPath)
	}
	a, ok := src.(identifier)
	if !ok {
		return false
	}
	b, ok := dst.(identifier)
	if !ok {
		return false
	}
	ida, err := a.identity(srcPath)
	if err != nil {
		return false
	}
	idb, err := b.identity(dstPath)
	if err != nil || ida != idb {
		return false
	}
	if s, ok := src.(*SFTP); ok {
		if d, ok := dst.(*SFTP); ok {
			return s.sameTree(d, dstPath)
		}
	}
	return true
}

// CopyFile streams one file from src to dst. The destination is committed
// only after every byte arrived; a failure leaves nothing at dstPath. A
// copy onto its own source is refused: the callers delete the source after
// a move, and that would delete the only copy.
func CopyFile(ctx context.Context, src Backend, srcPath string, dst Backend, dstPath string, opt CopyOptions) (CopyResult, error) {
	if sameFile(src, srcPath, dst, dstPath) {
		return CopyResult{}, fmt.Errorf("source and destination are the same file (%s)", dstPath)
	}
	rc, entry, err := src.Open(ctx, srcPath)
	if err != nil {
		return CopyResult{}, fmt.Errorf("open source %s: %w", srcPath, err)
	}
	defer rc.Close()

	w, err := dst.Create(ctx, dstPath, opt.Overwrite)
	if err != nil {
		if errors.Is(err, ErrExists) {
			return CopyResult{}, existsError(dstPath)
		}
		return CopyResult{}, fmt.Errorf("create destination %s: %w", dstPath, err)
	}

	h := sha256.New()
	pw := &progressWriter{fn: opt.Progress}
	n, err := copyCtx(ctx, io.MultiWriter(w, h, pw), rc)
	if err != nil {
		_ = w.Abort()
		if errors.Is(err, ErrExists) {
			return CopyResult{Bytes: n}, existsError(dstPath)
		}
		return CopyResult{Bytes: n}, err
	}
	var mtime time.Time
	if opt.PreserveMTime {
		mtime = entry.ModTime
	}
	if err := w.Commit(mtime); err != nil {
		if errors.Is(err, ErrExists) {
			return CopyResult{Bytes: n}, existsError(dstPath)
		}
		return CopyResult{Bytes: n}, fmt.Errorf("commit destination %s: %w", dstPath, err)
	}
	res := CopyResult{Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil)), Entry: entry}
	if opt.Verify {
		got, err := hashOf(ctx, dst, dstPath)
		if err != nil {
			return res, fmt.Errorf("verify %s: %w", dstPath, err)
		}
		if got != res.SHA256 {
			_ = dst.Remove(ctx, dstPath)
			return res, fmt.Errorf("verify %s: checksum mismatch after write", dstPath)
		}
	}
	return res, nil
}

func existsError(dstPath string) error {
	return fmt.Errorf("destination %s exists and overwrite is off", dstPath)
}

func hashOf(ctx context.Context, b Backend, p string) (string, error) {
	rc, _, err := b.Open(ctx, p)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := copyCtx(ctx, h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyCtx is io.Copy that stops when ctx is done, by closing the source.
func copyCtx(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			if c, ok := src.(io.Closer); ok {
				_ = c.Close()
			}
		case <-done:
		}
	}()
	n, err := io.Copy(dst, src)
	if ctx.Err() != nil {
		return n, ctx.Err()
	}
	return n, err
}

type progressWriter struct {
	fn    func(int64)
	last  time.Time
	total int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.total += int64(len(b))
	if p.fn != nil && time.Since(p.last) > time.Second {
		p.last = time.Now()
		p.fn(p.total)
	}
	return len(b), nil
}

// HumanBytes formats a byte count for logs.
func HumanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1024*1024*1024))
	}
}
