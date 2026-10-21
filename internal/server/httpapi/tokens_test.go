package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/httpapi"
	"github.com/zypher-systems/kairos/internal/testutil"
)

// doBearer is doJSON with an Authorization header instead of a cookie.
func doBearer(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "http://kairos.local"+path, rdr)
	req.Host = "kairos.local"
	req.Header.Set("Origin", "http://kairos.local")
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAPITokenCeilingAndMinting(t *testing.T) {
	h := testRouter(t)
	cookie := setupAdmin(t, h)

	w := doJSON(t, h, http.MethodPost, "/api/v1/tokens", cookie, map[string]any{"name": "ops", "role_ceiling": "operator"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var tok struct {
		Token       string  `json:"token"`
		RoleCeiling *string `json:"role_ceiling"`
		ExpiresAt   *string `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	if tok.RoleCeiling == nil || *tok.RoleCeiling != "operator" {
		t.Fatalf("ceiling %v", tok.RoleCeiling)
	}
	if tok.ExpiresAt != nil {
		t.Fatalf("no expiry requested, got %v", *tok.ExpiresAt)
	}

	// The operator token works for reads but cannot mint tokens at all,
	// so it can never climb back to the owner's admin role.
	if w := doBearer(t, h, http.MethodGet, "/api/v1/jobs", tok.Token, nil); w.Code != http.StatusOK {
		t.Fatalf("token read: %d %s", w.Code, w.Body.String())
	}
	w = doBearer(t, h, http.MethodPost, "/api/v1/tokens", tok.Token, map[string]any{"name": "escalate"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("token minting via token must be forbidden: %d %s", w.Code, w.Body.String())
	}

	// A session with a bounded role cannot mint above itself either.
	w = doJSON(t, h, http.MethodPost, "/api/v1/tokens", cookie, map[string]any{"name": "cap", "role_ceiling": "viewer"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create viewer: %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)
	if w := doBearer(t, h, http.MethodPost, "/api/v1/jobs", tok.Token, map[string]any{"name": "x"}); w.Code != http.StatusForbidden {
		t.Fatalf("viewer token must not write: %d", w.Code)
	}
}

func TestAPITokenExpires(t *testing.T) {
	// testutil.Store holds a mutex per test, so take the store once here
	// and build the router around it instead of calling testRouter.
	st := testutil.Store(t)
	h := httpapi.New(&config.Config{Secret: "kairos-test-secret-not-for-production-32b"}, nil, st, nil, nil).Router()
	cookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPost, "/api/v1/tokens", cookie, map[string]any{"name": "short", "expires_in_days": 1})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var tok struct {
		ID        string  `json:"id"`
		Token     string  `json:"token"`
		ExpiresAt *string `json:"expires_at"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)
	if tok.ExpiresAt == nil {
		t.Fatal("expires_at must be set")
	}
	if w := doBearer(t, h, http.MethodGet, "/api/v1/jobs", tok.Token, nil); w.Code != http.StatusOK {
		t.Fatalf("fresh token: %d", w.Code)
	}
	if _, err := st.Pool().Exec(t.Context(), `UPDATE api_tokens SET expires_at = now() - interval '1 minute' WHERE id = $1`, tok.ID); err != nil {
		t.Fatal(err)
	}
	if w := doBearer(t, h, http.MethodGet, "/api/v1/jobs", tok.Token, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired token must be rejected: %d", w.Code)
	}
	if w := doJSON(t, h, http.MethodPost, "/api/v1/tokens", cookie, map[string]any{"name": "bad", "expires_in_days": 0}); w.Code != http.StatusBadRequest {
		t.Fatalf("expires_in_days 0 must be rejected: %d", w.Code)
	}
}

func TestAgentUploadNeedsAClaimedTransfer(t *testing.T) {
	h := testRouter(t)
	cookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPost, "/api/v1/agents", cookie, map[string]any{"name": "w1"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create agent: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	req := httptest.NewRequest(http.MethodPost, "http://kairos.local/api/v1/agent/files?name=x&run_id=6f1c2b1e-0000-4000-8000-000000000001", strings.NewReader("payload"))
	req.Host = "kairos.local"
	req.Header.Set("Authorization", "Bearer "+created.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("upload without a claimed transfer run must be refused: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "http://kairos.local/api/v1/agent/files?name=x", strings.NewReader("payload"))
	req.Host = "kairos.local"
	req.Header.Set("Authorization", "Bearer "+created.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("upload without run_id must be refused: %d", rec.Code)
	}
}
