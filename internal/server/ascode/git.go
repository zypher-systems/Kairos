package ascode

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ClonePull clones (depth 1) a git repo into a temp dir and returns the path
// plus the HEAD sha. Caller must RemoveAll the dir.
func ClonePull(ctx context.Context, repoURL, ref, token string) (dir, sha string, err error) {
	u, err := parseGitURL(repoURL)
	if err != nil {
		return "", "", err
	}
	cloneURL := u.String()
	// The token travels in git's environment as an extra HTTP header, never
	// in the clone URL: argv is visible to every process on the host.
	var authEnv []string
	if token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		authEnv = []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http." + u.Scheme + "://" + u.Host + "/.extraheader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic " + basic,
		}
	}
	dir, err = os.MkdirTemp("", "kairos-git-*")
	if err != nil {
		return "", "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()
	if ref == "" {
		ref = "main"
	}
	args := []string{"clone", "--depth", "1", "--branch", ref, "--single-branch", "--", cloneURL, dir}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=echo"), authEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git clone: %s", redact(string(out), token, cloneURL, repoURL))
	}
	shaCmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	shaOut, err := shaCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse: %w", err)
	}
	cleanup = false
	return dir, strings.TrimSpace(string(shaOut)), nil
}

func parseGitURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("git url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid git url")
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
	default:
		return nil, fmt.Errorf("git url must be http or https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("git url has no host")
	}
	return u, nil
}

// CollectYAML reads .yaml/.yml files under root/rel.
func CollectYAML(root, rel string) ([]byte, error) {
	base := root
	rel = strings.TrimSpace(rel)
	if rel != "" && rel != "." {
		base = filepath.Join(root, filepath.Clean(rel))
	}
	info, err := os.Stat(base)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		b, err := os.ReadFile(base)
		return b, err
	}
	var chunks [][]byte
	err = filepath.Walk(base, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			if fi.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(fi.Name()))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		chunks = append(chunks, b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return joinDocs(chunks), nil
}

func joinDocs(chunks [][]byte) []byte {
	var b strings.Builder
	for i, c := range chunks {
		s := strings.TrimSpace(string(c))
		if s == "" {
			continue
		}
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(s)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func redact(s, token, cloneURL, publicURL string) string {
	s = strings.TrimSpace(s)
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	if cloneURL != "" && publicURL != "" {
		s = strings.ReplaceAll(s, cloneURL, publicURL)
	}
	if len(s) > 800 {
		s = s[:800] + "…"
	}
	return s
}

// SyncTimeout is the clone budget.
const SyncTimeout = 2 * time.Minute
