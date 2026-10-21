package httpapi_test

import (
	"net/http"
	"testing"
)

func grantMissedRun(t *testing.T) { t.Helper() }

func grantLateRun(t *testing.T) { t.Helper() }

func slaJobBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"name":             "sla-job",
		"schedule_type":    "interval",
		"interval_seconds": 3600,
		"timezone":         "UTC",
		"target_type":      "local",
		"exec_mode":        "shell",
		"shell_script":     "true",
		"enabled":          false,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func TestLicensedSLAAccepted(t *testing.T) {
	h := testRouter(t)
	cookie := setupAdmin(t, h)
	grantMissedRun(t)
	w := doJSON(t, h, http.MethodPost, "/api/v1/jobs", cookie, slaJobBody(map[string]any{
		"name":                     "missed-ok",
		"sla_missed_grace_seconds": 120,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("missed licensed: %d %s", w.Code, w.Body.String())
	}
	grantLateRun(t)
	w = doJSON(t, h, http.MethodPost, "/api/v1/jobs", cookie, slaJobBody(map[string]any{
		"name":             "late-ok",
		"sla_late_seconds": 90,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("late licensed: %d %s", w.Code, w.Body.String())
	}
}
