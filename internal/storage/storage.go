// Package storage gives file transfers and file triggers one view of the
// places files live: a directory on a machine (a local disk or a mounted
// NFS/SMB share), an SFTP host, or an S3-compatible bucket. The server and
// the agent both use it, so a transfer runs wherever it is cheapest
// without the Kairos server in the data path.
//
// Paths handed to a Backend are relative to its root when the Spec has
// one, and must stay inside it. A Spec without a root (legacy SSH and
// agent endpoints) takes absolute paths.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Kind is a backend type.
type Kind string

const (
	KindLocal Kind = "local" // a directory on the machine running the transfer
	KindSFTP  Kind = "sftp"
	KindS3    Kind = "s3"
	KindNFS   Kind = "nfs" // an NFSv3 export, spoken directly (no mount)
	KindSMB   Kind = "smb" // an SMB2/3 share, spoken directly (no mount)
)

// Spec is everything needed to open a backend, secrets included. It is
// built by the server from a storage location (or a legacy endpoint) and
// travels to an agent inside its run spec over the authenticated channel.
type Spec struct {
	Kind Kind `json:"kind"`
	// Root is the base directory (local, sftp) or key prefix (s3). Empty
	// means absolute paths.
	Root string `json:"root,omitempty"`

	// sftp
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	Username           string `json:"username,omitempty"`
	PrivateKeyPEM      string `json:"private_key_pem,omitempty"`
	Password           string `json:"password,omitempty"`
	HostKeyFingerprint string `json:"host_key_fingerprint,omitempty"`
	VerifyHostKey      bool   `json:"verify_host_key,omitempty"`

	// nfs: Host is the server, Export the exported path; UID/GID are the
	// AUTH_SYS identity the server sees.
	Export string `json:"export,omitempty"`
	UID    uint32 `json:"uid,omitempty"`
	GID    uint32 `json:"gid,omitempty"`

	// smb: Host is the server, Bucket the share name, Username/Password/
	// Domain the account.
	Domain string `json:"domain,omitempty"`

	// s3
	Endpoint  string `json:"endpoint,omitempty"` // host[:port]; empty means AWS
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style,omitempty"`
	Insecure  bool   `json:"insecure,omitempty"` // plain http endpoint
}

// Secrets lists the values in the spec that a log must never show.
func (s Spec) Secrets() []string {
	var out []string
	for _, v := range []string{s.PrivateKeyPEM, s.Password, s.SecretKey} {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Describe names the backend for logs without its secrets.
func (s Spec) Describe() string {
	switch s.Kind {
	case KindLocal:
		if s.Root == "" {
			return "local filesystem"
		}
		return "directory " + s.Root
	case KindSFTP:
		return fmt.Sprintf("sftp %s@%s:%d %s", s.Username, s.Host, s.Port, s.Root)
	case KindS3:
		ep := s.Endpoint
		if ep == "" {
			ep = "s3"
		}
		return fmt.Sprintf("%s/%s/%s", ep, s.Bucket, s.Root)
	case KindNFS:
		return fmt.Sprintf("nfs %s:%s/%s", s.Host, s.Export, s.Root)
	case KindSMB:
		return fmt.Sprintf("smb //%s/%s/%s", s.Host, s.Bucket, s.Root)
	}
	return string(s.Kind)
}

// Entry is one file a backend knows about.
type Entry struct {
	// Path is relative to the backend root (or absolute for rootless specs).
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir,omitempty"`
}

// Writer receives one file. Commit makes it visible at its final path;
// Abort discards it. Exactly one of them must be called.
type Writer interface {
	io.Writer
	Commit(modTime time.Time) error
	Abort() error
}

// Backend is one place files live.
type Backend interface {
	Kind() Kind
	// List returns the files matching pattern (path.Match syntax on the
	// last element; earlier elements are literal). A pattern with no
	// wildcard returns that one file when it exists.
	List(ctx context.Context, pattern string) ([]Entry, error)
	Stat(ctx context.Context, p string) (Entry, error)
	Open(ctx context.Context, p string) (io.ReadCloser, Entry, error)
	// Create prepares p for writing. With overwrite false an existing
	// file is an error. Where the backend supports it the data lands in a
	// temporary file and is renamed into place on Commit.
	Create(ctx context.Context, p string, overwrite bool) (Writer, error)
	Remove(ctx context.Context, p string) error
	Close() error
}

// ErrExists is returned by Create when the target exists and overwrite is off.
var ErrExists = errors.New("destination exists")

// ErrNotFound is returned for a missing file.
var ErrNotFound = errors.New("file not found")

// Open builds the backend for a spec.
func Open(ctx context.Context, s Spec) (Backend, error) {
	switch s.Kind {
	case KindLocal:
		return NewLocal(s.Root)
	case KindSFTP:
		return OpenSFTP(ctx, s)
	case KindS3:
		return OpenS3(ctx, s)
	case KindNFS:
		return OpenNFS(ctx, s)
	case KindSMB:
		return OpenSMB(ctx, s)
	}
	return nil, fmt.Errorf("unknown storage kind %q", s.Kind)
}

// HasGlob reports whether p contains a wildcard.
func HasGlob(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

// literalPrefix returns the part of a pattern before the first wildcard
// element, so a listing can be narrowed before matching.
func literalPrefix(pattern string) string {
	if !HasGlob(pattern) {
		return pattern
	}
	i := strings.IndexAny(pattern, "*?[")
	dir := pattern[:i]
	if j := strings.LastIndex(dir, "/"); j >= 0 {
		return dir[:j+1]
	}
	return ""
}

// cleanRel normalises a path relative to a root and refuses escapes. With
// an empty root the path is returned cleaned but otherwise as given.
func cleanRel(root, p string) (string, error) {
	p = strings.TrimSpace(p)
	if root == "" {
		if p == "" {
			return "", errors.New("path is required")
		}
		return path.Clean(p), nil
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path %q escapes the location", p)
		}
	}
	p = strings.TrimPrefix(p, "/")
	c := path.Clean("/" + p)
	if c == "/" {
		return "", errors.New("path is required")
	}
	return strings.TrimPrefix(c, "/"), nil
}

// joinRoot resolves a cleaned relative path under root using / separators.
func joinRoot(root, rel string) string {
	if root == "" {
		return rel
	}
	return strings.TrimRight(root, "/") + "/" + rel
}

// DestPath decides where one source file lands: dest as given, or, when
// the source was a glob or dest names a directory (trailing slash), the
// source's base name inside dest.
func DestPath(srcPattern, srcMatch, dest string) string {
	if HasGlob(srcPattern) || strings.HasSuffix(dest, "/") || dest == "" || dest == "." {
		base := path.Base(srcMatch)
		if dest == "" || dest == "." {
			return base
		}
		return strings.TrimRight(dest, "/") + "/" + base
	}
	return dest
}
