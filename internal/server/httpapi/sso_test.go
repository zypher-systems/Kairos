package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/httpapi"
	"github.com/zypher-systems/kairos/internal/server/oidcauth"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func grantSSO(t *testing.T) { t.Helper() }

func TestSSOCallbackCreatesSessionForLinkedUser(t *testing.T) {
	grantSSO(t)
	st := testutil.Store(t)
	api := httpapi.New(&config.Config{Secret: "kairos-test-secret-not-for-production-32b"}, nil, st, nil, nil)
	fake := &oidcauth.Fake{Subject: "idp-root", AuthURL: "https://idp.example/authorize"}
	api.SetOIDCProvider(fake)
	h := api.Router()

	cookie := setupAdmin(t, h)
	link := doJSON(t, h, http.MethodPut, "/api/v1/users/"+userIDFromList(t, h, cookie, "root"), cookie, map[string]any{
		"role":         "admin",
		"team":         "",
		"enabled":      true,
		"oidc_subject": "idp-root",
	})
	if link.Code != http.StatusOK {
		t.Fatalf("link %d %s", link.Code, link.Body.String())
	}
	save := doJSON(t, h, http.MethodPut, "/api/v1/settings", cookie, map[string]any{
		"default_timezone":  "UTC",
		"sso_enabled":       true,
		"sso_issuer":        "https://idp.example",
		"sso_client_id":     "kairos",
		"sso_client_secret": "s3cret",
		"sso_redirect_url":  "http://kairos.local/api/v1/auth/sso/callback",
	})
	if save.Code != http.StatusOK {
		t.Fatalf("settings %d %s", save.Code, save.Body.String())
	}

	status := doJSON(t, h, http.MethodGet, "/api/v1/auth/status", "", nil)
	var stBody struct {
		SSOAvailable bool `json:"sso_available"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &stBody); err != nil {
		t.Fatal(err)
	}
	if !stBody.SSOAvailable {
		t.Fatalf("status should advertise SSO: %s", status.Body.String())
	}

	start := doJSON(t, h, http.MethodPost, "/api/v1/auth/sso/start", "", nil)
	if start.Code != http.StatusOK {
		t.Fatalf("start %d %s", start.Code, start.Body.String())
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("missing state in %s", body.URL)
	}

	cb := httptest.NewRequest(http.MethodGet, "http://kairos.local/api/v1/auth/sso/callback?code=ok&state="+url.QueryEscape(state), nil)
	cb.Host = "kairos.local"
	for _, c := range start.Result().Cookies() {
		cb.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, cb)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
	}
	sess := sessionCookie(t, rec)
	me := doJSON(t, h, http.MethodGet, "/api/v1/auth/status", sess, nil)
	var meBody struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username"`
		Role          string `json:"role"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &meBody); err != nil {
		t.Fatal(err)
	}
	if !meBody.Authenticated || meBody.Username != "root" || meBody.Role != "admin" {
		t.Fatalf("session %+v", meBody)
	}
}

func TestSSOCallbackRefusesUnlinked(t *testing.T) {
	grantSSO(t)
	st := testutil.Store(t)
	api := httpapi.New(&config.Config{Secret: "kairos-test-secret-not-for-production-32b"}, nil, st, nil, nil)
	fake := &oidcauth.Fake{Subject: "unknown-sub", AuthURL: "https://idp.example/authorize"}
	api.SetOIDCProvider(fake)
	h := api.Router()
	cookie := setupAdmin(t, h)
	save := doJSON(t, h, http.MethodPut, "/api/v1/settings", cookie, map[string]any{
		"default_timezone":  "UTC",
		"sso_enabled":       true,
		"sso_issuer":        "https://idp.example",
		"sso_client_id":     "kairos",
		"sso_client_secret": "s3cret",
		"sso_redirect_url":  "http://kairos.local/api/v1/auth/sso/callback",
	})
	if save.Code != http.StatusOK {
		t.Fatalf("settings %d %s", save.Code, save.Body.String())
	}
	start := doJSON(t, h, http.MethodPost, "/api/v1/auth/sso/start", "", nil)
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	cb := httptest.NewRequest(http.MethodGet, "http://kairos.local/api/v1/auth/sso/callback?code=ok&state="+url.QueryEscape(state), nil)
	cb.Host = "kairos.local"
	for _, c := range start.Result().Cookies() {
		cb.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, cb)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback %d %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "sso_error=unlinked") {
		t.Fatalf("want unlinked redirect, got %s", loc)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "kairos_session" && c.Value != "" {
			t.Fatal("must not issue a session for an unlinked identity")
		}
	}
}

func userIDFromList(t *testing.T, h http.Handler, cookie, username string) string {
	t.Helper()
	w := doJSON(t, h, http.MethodGet, "/api/v1/users", cookie, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list users %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, u := range body.Users {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("user %s not in %s", username, w.Body.String())
	return ""
}

// A callback in a browser that did not start the sign-in (no or foreign
// SSO cookie) is refused: this blocks login CSRF.
func TestSSOCallbackRequiresTheStartingBrowser(t *testing.T) {
	grantSSO(t)
	st := testutil.Store(t)
	api := httpapi.New(&config.Config{Secret: "kairos-test-secret-not-for-production-32b"}, nil, st, nil, nil)
	api.SetOIDCProvider(&oidcauth.Fake{Subject: "sub-root", AuthURL: "https://idp.example/authorize"})
	h := api.Router()
	cookie := setupAdmin(t, h)
	save := doJSON(t, h, http.MethodPut, "/api/v1/settings", cookie, map[string]any{
		"sso_issuer":        "https://idp.example",
		"sso_client_id":     "kairos",
		"sso_client_secret": "s3cret",
		"sso_redirect_url":  "http://kairos.local/api/v1/auth/sso/callback",
	})
	if save.Code != http.StatusOK && save.Code != http.StatusNoContent {
		t.Fatalf("save settings %d %s", save.Code, save.Body.String())
	}
	start := doJSON(t, h, http.MethodPost, "/api/v1/auth/sso/start", "", nil)
	if start.Code != http.StatusOK || len(start.Result().Cookies()) == 0 {
		t.Fatalf("start %d (cookies %d) %s", start.Code, len(start.Result().Cookies()), start.Body.String())
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &body)
	u, _ := url.Parse(body.URL)
	cb := httptest.NewRequest(http.MethodGet, "http://kairos.local/api/v1/auth/sso/callback?code=ok&state="+url.QueryEscape(u.Query().Get("state")), nil)
	cb.Host = "kairos.local"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, cb)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "sso_error=denied") {
		t.Fatalf("callback without the start cookie must be denied, got %d %s", rec.Code, loc)
	}
}
