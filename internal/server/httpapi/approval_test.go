package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func grantApprovalGates(t *testing.T, extra ...string) { t.Helper() }

func approvalJobBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"name":             "gated-job",
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

func TestLicensedGatedRunApprove(t *testing.T) {
	grantApprovalGates(t)
	h := testRouter(t)
	cookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPost, "/api/v1/jobs", cookie, approvalJobBody(map[string]any{
		"name":             "hold-me",
		"require_approval": true,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var job map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	id := job["id"].(string)
	runW := doJSON(t, h, http.MethodPost, "/api/v1/jobs/"+id+"/run", cookie, nil)
	if runW.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", runW.Code, runW.Body.String())
	}
	var run map[string]any
	if err := json.Unmarshal(runW.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run["status"] != "pending_approval" {
		t.Fatalf("status %v", run["status"])
	}
	runID := run["id"].(string)
	again := doJSON(t, h, http.MethodPost, "/api/v1/runs/"+runID+"/approve", cookie, nil)
	if again.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", again.Code, again.Body.String())
	}
	var approved map[string]any
	if err := json.Unmarshal(again.Body.Bytes(), &approved); err != nil {
		t.Fatal(err)
	}
	if approved["status"] != "queued" {
		t.Fatalf("after approve %v", approved["status"])
	}
	conflict := doJSON(t, h, http.MethodPost, "/api/v1/runs/"+runID+"/approve", cookie, nil)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("second approve: %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestViewerCannotApprove(t *testing.T) {
	grantApprovalGates(t, "multi_user")
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
	jw := doJSON(t, h, http.MethodPost, "/api/v1/jobs", adminCookie, approvalJobBody(map[string]any{
		"name":             "viewer-gate",
		"require_approval": true,
	}))
	if jw.Code != http.StatusCreated {
		t.Fatalf("job: %d %s", jw.Code, jw.Body.String())
	}
	var job map[string]any
	_ = json.Unmarshal(jw.Body.Bytes(), &job)
	runW := doJSON(t, h, http.MethodPost, "/api/v1/jobs/"+job["id"].(string)+"/run", adminCookie, nil)
	var run map[string]any
	_ = json.Unmarshal(runW.Body.Bytes(), &run)
	login := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "view",
		"password": "view-pass-123",
	})
	viewCookie := sessionCookie(t, login)
	denied := doJSON(t, h, http.MethodPost, "/api/v1/runs/"+run["id"].(string)+"/approve", viewCookie, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("viewer approve: %d %s", denied.Code, denied.Body.String())
	}
}

func TestOperatorCanApprove(t *testing.T) {
	grantApprovalGates(t, "multi_user")
	h := testRouter(t)
	adminCookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPost, "/api/v1/users", adminCookie, map[string]string{
		"username": "ops",
		"password": "ops-pass-123",
		"role":     "operator",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create operator: %d %s", w.Code, w.Body.String())
	}
	jw := doJSON(t, h, http.MethodPost, "/api/v1/jobs", adminCookie, approvalJobBody(map[string]any{
		"name":             "ops-gate",
		"require_approval": true,
	}))
	var job map[string]any
	_ = json.Unmarshal(jw.Body.Bytes(), &job)
	runW := doJSON(t, h, http.MethodPost, "/api/v1/jobs/"+job["id"].(string)+"/run", adminCookie, nil)
	var run map[string]any
	_ = json.Unmarshal(runW.Body.Bytes(), &run)
	login := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "ops",
		"password": "ops-pass-123",
	})
	opsCookie := sessionCookie(t, login)
	ok := doJSON(t, h, http.MethodPost, "/api/v1/runs/"+run["id"].(string)+"/approve", opsCookie, nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("operator approve: %d %s", ok.Code, ok.Body.String())
	}
}
