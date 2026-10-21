package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/httpapi"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestMetricsTokenAndRegistry(t *testing.T) {
	st := testutil.Store(t)
	cfg := &config.Config{Secret: "kairos-test-secret-not-for-production-32b", MetricsToken: "scrape-me"}
	h := httpapi.New(cfg, nil, st, nil, nil).Router()

	req := httptest.NewRequest(http.MethodGet, "http://kairos.local/metrics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", w.Code)
	}
	req.Header.Set("Authorization", "Bearer scrape-me")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("with the token: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"kairos_info{version=",
		"kairos_runs_queued ",
		"# TYPE kairos_runs_started_total counter",
		"# TYPE kairos_http_requests_total counter",
		"kairos_http_requests_total{method=\"GET\",class=\"4xx\"} ",
		"kairos_notify_dead_letters ",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}
