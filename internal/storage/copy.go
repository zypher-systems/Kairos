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

// CopyFile streams one file from src to dst. The destination is committed
// only after every byte arrived; a failure leaves nothing at dstPath.
func CopyFile(ctx context.Context, src Backend, srcPath string, dst Backend, dstPath string, opt CopyOptions) (CopyResult, error) {
	rc, entry, err := src.Open(ctx, srcPath)
	if err != nil {
		return CopyResult{}, fmt.Errorf("open source %s: %w", srcPath, err)
	}
	defer rc.Close()

	w, err := dst.Create(ctx, dstPath, opt.Overwrite)
	if err != nil {
		if errors.Is(err, ErrExists) {
			return CopyResult{}, fmt.Errorf("destination %s exists and overwrite is off", dstPath)
		}
		return CopyResult{}, fmt.Errorf("create destination %s: %w", dstPath, err)
	}

	h := sha256.New()
	pw := &progressWriter{fn: opt.Progress}
	n, err := copyCtx(ctx, io.MultiWriter(w, h, pw), rc)
	if err != nil {
		_ = w.Abort()
		return CopyResult{Bytes: n}, err
	}
	var mtime time.Time
	if opt.PreserveMTime {
		mtime = entry.ModTime
	}
	if err := w.Commit(mtime); err != nil {
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
