package vaultkv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxBody = 1 << 20

// Request is a Vault KV v2 read.
type Request struct {
	Address   string
	Token     string
	Mount     string
	Namespace string
	Path      string
	Key       string
}

var defaultClient = &http.Client{Timeout: 10 * time.Second}

// Get reads one key from a Vault KV v2 secret. The HTTP body is never
// returned in errors.
func Get(ctx context.Context, client *http.Client, req Request) (string, error) {
	if client == nil {
		client = defaultClient
	}
	addr := strings.TrimRight(strings.TrimSpace(req.Address), "/")
	mount := strings.Trim(req.Mount, "/")
	path := strings.TrimPrefix(req.Path, "/")
	if addr == "" || req.Token == "" || mount == "" || path == "" || req.Key == "" {
		return "", fmt.Errorf("vault: incomplete request")
	}
	u := addr + "/v1/" + mount + "/data/" + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("vault: request")
	}
	httpReq.Header.Set("X-Vault-Token", req.Token)
	if ns := strings.TrimSpace(req.Namespace); ns != "" {
		httpReq.Header.Set("X-Vault-Namespace", ns)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("vault: unreachable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("vault: read failed")
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("vault: denied")
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("vault: not found")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vault: http %d", resp.StatusCode)
	}
	var parsed struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("vault: invalid response")
	}
	raw, ok := parsed.Data.Data[req.Key]
	if !ok || raw == nil {
		return "", fmt.Errorf("vault: key missing")
	}
	switch v := raw.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case float64:
		return fmt.Sprintf("%g", v), nil
	case bool:
		return fmt.Sprintf("%t", v), nil
	default:
		return "", fmt.Errorf("vault: key is not a string")
	}
}
