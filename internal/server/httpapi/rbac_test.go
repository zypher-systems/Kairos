package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/httpapi"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	st := testutil.Store(t)
	api := httpapi.New(&config.Config{Secret: "kairos-test-secret-not-for-production-32b"}, nil, st, nil, nil)
	return api.Router()
}

func doJSON(t *testing.T, h http.Handler, method, path, cookie string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, "http://kairos.local"+path, rdr)
	req.Host = "kairos.local"
	req.Header.Set("Origin", "http://kairos.local")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "kairos_session", Value: cookie})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "kairos_session" && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("missing session cookie, status %d body %s", w.Code, w.Body.String())
	return ""
}

func setupAdmin(t *testing.T, h http.Handler) string {
	t.Helper()
	w := doJSON(t, h, http.MethodPost, "/api/v1/auth/setup", "", map[string]string{
		"username": "root",
		"password": "root-pass-123",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("setup %d %s", w.Code, w.Body.String())
	}
	return sessionCookie(t, w)
}

func grantMultiUser(t *testing.T) { t.Helper() }

func TestViewerCannotWriteSettings(t *testing.T) {
	grantMultiUser(t)
	h := testRouter(t)
	adminCookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPost, "/api/v1/users", adminCookie, map[string]string{
		"username": "view",
		"password": "view-pass-123",
		"role":     "viewer",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create viewer: %d %s", w.Code, w.Body.String())
	}
	login := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "view",
		"password": "view-pass-123",
	})
	if login.Code != http.StatusOK {
		t.Fatalf("viewer login: %d %s", login.Code, login.Body.String())
	}
	viewCookie := sessionCookie(t, login)

	got := doJSON(t, h, http.MethodGet, "/api/v1/jobs", viewCookie, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("viewer GET jobs: %d %s", got.Code, got.Body.String())
	}
	denied := doJSON(t, h, http.MethodPut, "/api/v1/settings", viewCookie, map[string]any{
		"default_timezone": "UTC",
		"retention_runs":   50,
		"retention_days":   7,
	})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("viewer PUT settings: %d %s", denied.Code, denied.Body.String())
	}
	job := doJSON(t, h, http.MethodPost, "/api/v1/jobs", viewCookie, map[string]any{
		"name":             "nope",
		"schedule_type":    "interval",
		"interval_seconds": 3600,
		"timezone":         "UTC",
		"target_type":      "local",
		"exec_mode":        "shell",
		"shell_script":     "true",
	})
	if job.Code != http.StatusForbidden {
		t.Fatalf("viewer POST job: %d %s", job.Code, job.Body.String())
	}
}

func TestAuthStatusIncludesRole(t *testing.T) {
	h := testRouter(t)
	cookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodGet, "/api/v1/auth/status", cookie, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username"`
		Role          string `json:"role"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Authenticated || body.Username != "root" || body.Role != "admin" {
		t.Fatalf("status %+v", body)
	}
}
