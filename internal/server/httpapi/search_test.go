package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/httpapi"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func grantLongRetention(t *testing.T) { t.Helper() }

func grantLogSearch(t *testing.T) { t.Helper() }

func TestLongRetentionAllowsHigherCaps(t *testing.T) {
	grantLongRetention(t)
	h := testRouter(t)
	cookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPut, "/api/v1/settings", cookie, map[string]any{
		"default_timezone": "UTC",
		"retention_runs":   500,
		"retention_days":   90,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("licensed retention %d %s", w.Code, w.Body.String())
	}
}

func TestLogSearchFindsLineAndJobFilter(t *testing.T) {
	grantLogSearch(t)
	st := testutil.Store(t)
	api := httpapi.New(&config.Config{Secret: "kairos-test-secret-not-for-production-32b"}, nil, st, nil, nil)
	h := api.Router()
	cookie := setupAdmin(t, h)

	jobA := doJSON(t, h, http.MethodPost, "/api/v1/jobs", cookie, map[string]any{
		"name":             "search-a",
		"schedule_type":    "interval",
		"interval_seconds": 3600,
		"timezone":         "UTC",
		"target_type":      "local",
		"exec_mode":        "shell",
		"shell_script":     "true",
		"enabled":          false,
	})
	if jobA.Code != http.StatusCreated {
		t.Fatalf("job a %d %s", jobA.Code, jobA.Body.String())
	}
	var ja store.Job
	if err := json.Unmarshal(jobA.Body.Bytes(), &ja); err != nil {
		t.Fatal(err)
	}
	jobB := doJSON(t, h, http.MethodPost, "/api/v1/jobs", cookie, map[string]any{
		"name":             "search-b",
		"schedule_type":    "interval",
		"interval_seconds": 3600,
		"timezone":         "UTC",
		"target_type":      "local",
		"exec_mode":        "shell",
		"shell_script":     "true",
		"enabled":          false,
	})
	var jb store.Job
	if err := json.Unmarshal(jobB.Body.Bytes(), &jb); err != nil {
		t.Fatal(err)
	}

	runA := doJSON(t, h, http.MethodPost, "/api/v1/jobs/"+ja.ID.String()+"/run", cookie, nil)
	if runA.Code != http.StatusAccepted {
		t.Fatalf("run a %d %s", runA.Code, runA.Body.String())
	}
	var ra store.Run
	if err := json.Unmarshal(runA.Body.Bytes(), &ra); err != nil {
		t.Fatal(err)
	}
	runB := doJSON(t, h, http.MethodPost, "/api/v1/jobs/"+jb.ID.String()+"/run", cookie, nil)
	var rb store.Run
	if err := json.Unmarshal(runB.Body.Bytes(), &rb); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := st.AppendLog(ctx, ra.ID, 1, "stdout", "unique-needle-kairos in job a"); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendLog(ctx, rb.ID, 1, "stdout", "unique-needle-kairos in job b"); err != nil {
		t.Fatal(err)
	}

	all := doJSON(t, h, http.MethodGet, "/api/v1/logs/search?q=unique-needle-kairos", cookie, nil)
	if all.Code != http.StatusOK {
		t.Fatalf("search all %d %s", all.Code, all.Body.String())
	}
	var body struct {
		Hits []store.LogSearchHit `json:"hits"`
	}
	if err := json.Unmarshal(all.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Hits) != 2 {
		t.Fatalf("want 2 hits, got %d %s", len(body.Hits), all.Body.String())
	}

	one := doJSON(t, h, http.MethodGet, "/api/v1/logs/search?q=unique-needle-kairos&job_id="+ja.ID.String(), cookie, nil)
	if one.Code != http.StatusOK {
		t.Fatalf("search job %d %s", one.Code, one.Body.String())
	}
	body.Hits = nil
	if err := json.Unmarshal(one.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Hits) != 1 || body.Hits[0].JobID != ja.ID {
		t.Fatalf("job filter: %+v", body.Hits)
	}
	if body.Hits[0].RunID == uuid.Nil || body.Hits[0].Snippet == "" {
		t.Fatalf("hit incomplete: %+v", body.Hits[0])
	}
}
