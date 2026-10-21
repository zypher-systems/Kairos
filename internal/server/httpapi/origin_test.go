package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/config"
)

func TestGuardOrigin(t *testing.T) {
	api := &API{cfg: &config.Config{}}
	h := api.guardOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	post := func(origin, auth, fetchSite string) int {
		r := httptest.NewRequest(http.MethodPost, "http://kairos.local:8080/api/v1/jobs", nil)
		r.Host = "kairos.local:8080"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		if fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	if got := post("", "", ""); got != http.StatusForbidden {
		t.Fatalf("bare POST without Origin: %d", got)
	}
	if got := post("", "Bearer agent-token", ""); got != http.StatusNoContent {
		t.Fatalf("bearer without Origin must pass: %d", got)
	}
	if got := post("", "", "same-origin"); got != http.StatusNoContent {
		t.Fatalf("Sec-Fetch-Site same-origin must pass: %d", got)
	}
	if got := post("http://kairos.local:8080", "", ""); got != http.StatusNoContent {
		t.Fatalf("same host Origin must pass: %d", got)
	}
	if got := post("https://evil.example", "", ""); got != http.StatusForbidden {
		t.Fatalf("foreign Origin must fail: %d", got)
	}
}

func TestGuardOriginPublicURL(t *testing.T) {
	api := &API{cfg: &config.Config{PublicURL: "https://kairos.example.com"}}
	h := api.guardOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/jobs", nil)
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Origin", "https://kairos.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUBLIC_URL origin behind proxy: %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/jobs", nil)
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin with PUBLIC_URL set: %d", w.Code)
	}
}
