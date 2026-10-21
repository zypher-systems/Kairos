package vaultkv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetKV2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "s.tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path != "/v1/secret/data/jobs/foo" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"api_key": "from-vault"}},
		})
	}))
	t.Cleanup(srv.Close)

	got, err := Get(context.Background(), srv.Client(), Request{
		Address: srv.URL,
		Token:   "s.tok",
		Mount:   "secret",
		Path:    "jobs/foo",
		Key:     "api_key",
	})
	if err != nil || got != "from-vault" {
		t.Fatalf("get: %q %v", got, err)
	}
}

func TestGetDeniedAndMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "denied") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"other": "x"}},
		})
	}))
	t.Cleanup(srv.Close)

	_, err := Get(context.Background(), srv.Client(), Request{
		Address: srv.URL, Token: "t", Mount: "secret", Path: "denied", Key: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("denied: %v", err)
	}
	_, err = Get(context.Background(), srv.Client(), Request{
		Address: srv.URL, Token: "t", Mount: "secret", Path: "jobs/foo", Key: "api_key",
	})
	if err == nil || !strings.Contains(err.Error(), "key missing") {
		t.Fatalf("missing: %v", err)
	}
}
