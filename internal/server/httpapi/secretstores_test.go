package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/secrets"
)

func grantSecretStores(t *testing.T, extra ...string) { t.Helper() }

func storeJobBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"name":             "env-job",
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

func TestLicensedStoreCRUDAndAgentEnv(t *testing.T) {
	grantSecretStores(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"api_key": "from-vault"}},
		})
	}))
	t.Cleanup(srv.Close)
	secrets.HTTPClient = srv.Client()
	t.Cleanup(func() { secrets.HTTPClient = nil })

	h := testRouter(t)
	cookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPost, "/api/v1/secret-stores", cookie, map[string]any{
		"name":    "prod-vault",
		"type":    "vault",
		"address": srv.URL,
		"mount":   "secret",
		"token":   "s.tok",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create store: %d %s", w.Code, w.Body.String())
	}
	var ss map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	if ss["token"] != "••••••" {
		t.Fatalf("token should be masked, got %#v", ss["token"])
	}
	storeID := ss["id"].(string)

	aw := doJSON(t, h, http.MethodPost, "/api/v1/agents", cookie, map[string]any{"name": "worker"})
	if aw.Code != http.StatusCreated {
		t.Fatalf("agent: %d %s", aw.Code, aw.Body.String())
	}
	var createdAgent struct {
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(aw.Body.Bytes(), &createdAgent); err != nil {
		t.Fatal(err)
	}

	jw := doJSON(t, h, http.MethodPost, "/api/v1/jobs", cookie, storeJobBody(map[string]any{
		"name":            "agent-store",
		"target_type":     "agent",
		"target_selector": map[string]string{"agent_id": createdAgent.Agent.ID},
		"env_secret_refs": []map[string]any{{
			"name": "API_KEY", "store_id": storeID, "path": "jobs/foo", "key": "api_key",
		}},
	}))
	if jw.Code != http.StatusCreated {
		t.Fatalf("create job: %d %s", jw.Code, jw.Body.String())
	}
	var job map[string]any
	if err := json.Unmarshal(jw.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	run := doJSON(t, h, http.MethodPost, "/api/v1/jobs/"+job["id"].(string)+"/run", cookie, nil)
	if run.Code != http.StatusAccepted && run.Code != http.StatusOK && run.Code != http.StatusCreated {
		t.Fatalf("run now: %d %s", run.Code, run.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "http://kairos.local/api/v1/agent/claim", nil)
	req.Host = "kairos.local"
	req.Header.Set("Origin", "http://kairos.local")
	req.Header.Set("Authorization", "Bearer "+createdAgent.Token)
	cw := httptest.NewRecorder()
	h.ServeHTTP(cw, req)
	if cw.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", cw.Code, cw.Body.String())
	}
	var claim map[string]any
	if err := json.Unmarshal(cw.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	runSpec, _ := claim["run"].(map[string]any)
	env, _ := runSpec["env"].(map[string]any)
	if env["API_KEY"] != "from-vault" {
		t.Fatalf("agent env %+v body %s", env, cw.Body.String())
	}
}

func TestExportOmitsVaultToken(t *testing.T) {
	grantSecretStores(t, "jobs_as_code")
	h := testRouter(t)
	cookie := setupAdmin(t, h)
	w := doJSON(t, h, http.MethodPost, "/api/v1/secret-stores", cookie, map[string]any{
		"name":    "prod-vault",
		"address": "https://vault.example:8200",
		"mount":   "secret",
		"token":   "s.never-export",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	exp := doJSON(t, h, http.MethodGet, "/api/v1/export", cookie, nil)
	if exp.Code != http.StatusOK {
		t.Fatalf("export: %d %s", exp.Code, exp.Body.String())
	}
	body := exp.Body.String()
	if strings.Contains(body, "s.never-export") {
		t.Fatal("export leaked vault token")
	}
	if !strings.Contains(body, "kind: SecretStore") || !strings.Contains(body, "prod-vault") {
		t.Fatalf("export missing store: %s", body)
	}
}
