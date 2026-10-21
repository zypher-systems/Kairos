package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func grantJobsAsCode(t *testing.T) { t.Helper() }

func TestApplyDryRunWithLicense(t *testing.T) {
	grantJobsAsCode(t)
	h := testRouter(t)
	cookie := setupAdmin(t, h)
	yaml := strings.TrimSpace(`
apiVersion: kairos/v1
kind: Job
metadata:
  name: licensed-echo
  enabled: false
spec:
  schedule:
    type: interval
    interval_seconds: 3600
    timezone: UTC
  target:
    type: local
  exec:
    mode: shell
    script: echo hi
`)
	w := doJSON(t, h, http.MethodPost, "/api/v1/apply", cookie, map[string]any{
		"yaml":    yaml,
		"dry_run": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("dry-run %d %s", w.Code, w.Body.String())
	}
	w = doJSON(t, h, http.MethodPost, "/api/v1/apply", cookie, map[string]any{
		"yaml":    yaml,
		"dry_run": false,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("apply %d %s", w.Code, w.Body.String())
	}
	exp := doJSON(t, h, http.MethodGet, "/api/v1/export", cookie, nil)
	if exp.Code != http.StatusOK {
		t.Fatalf("export %d %s", exp.Code, exp.Body.String())
	}
	if !strings.Contains(exp.Body.String(), "licensed-echo") {
		t.Fatalf("export missing job: %s", exp.Body.String())
	}
}

func TestOpenAPIServes(t *testing.T) {
	h := testRouter(t)
	w := doJSON(t, h, http.MethodGet, "/api/v1/openapi.json", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("openapi %d %s", w.Code, w.Body.String())
	}
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.0.3" {
		t.Fatalf("openapi field %+v", spec["openapi"])
	}
}
