package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func grantCalendars(t *testing.T, extra ...string) { t.Helper() }

func calJobBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"name":             "cal-job",
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

func TestLicensedCalendarCRUDAndJobAttach(t *testing.T) {
	grantCalendars(t)
	h := testRouter(t)
	cookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPost, "/api/v1/calendars", cookie, map[string]any{
		"name":  "us-holidays",
		"dates": []string{"2026-12-25", "2027-01-01"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var cal map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &cal); err != nil {
		t.Fatal(err)
	}
	id := cal["id"].(string)
	w = doJSON(t, h, http.MethodPost, "/api/v1/jobs", cookie, calJobBody(map[string]any{
		"calendar_id": id,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("job attach: %d %s", w.Code, w.Body.String())
	}
	var job map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job["calendar_id"] != id {
		t.Fatalf("calendar_id %#v", job["calendar_id"])
	}
	w = doJSON(t, h, http.MethodPut, "/api/v1/calendars/"+id, cookie, map[string]any{
		"name":  "us-holidays",
		"dates": []string{"2026-12-25"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(t, h, http.MethodDelete, "/api/v1/calendars/"+id, cookie, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}
