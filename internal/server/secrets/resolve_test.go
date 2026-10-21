package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func grantStores(t *testing.T) { t.Helper() }

func TestResolveMergesVaultKey(t *testing.T) {
	grantStores(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"api_key": "from-vault"}},
		})
	}))
	t.Cleanup(srv.Close)
	HTTPClient = srv.Client()
	t.Cleanup(func() { HTTPClient = nil })

	st := testutil.Store(t)
	ctx := context.Background()
	ss := &store.SecretStore{Name: "v", Address: srv.URL, Mount: "secret", Token: "t"}
	if err := st.CreateSecretStore(ctx, ss); err != nil {
		t.Fatal(err)
	}
	job := store.Job{
		Name:     "j",
		EnvPlain: map[string]string{"PLAIN": "1"},
		EnvSecretRefs: []store.EnvSecretRef{
			{Name: "API_KEY", StoreID: ss.ID, Path: "jobs/foo", Key: "api_key"},
		},
	}
	if err := Resolve(ctx, st, &job); err != nil {
		t.Fatal(err)
	}
	if job.EnvSecrets["API_KEY"] != "from-vault" {
		t.Fatalf("env %+v", job.EnvSecrets)
	}
}

// Without the grant the run fails with a clear reason instead of running
// without its secrets (lapse is visible, never silent).
func TestResolveFailsOnDenied(t *testing.T) {
	grantStores(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	HTTPClient = srv.Client()
	t.Cleanup(func() { HTTPClient = nil })

	st := testutil.Store(t)
	ctx := context.Background()
	ss := &store.SecretStore{Name: "v", Address: srv.URL, Mount: "secret", Token: "t"}
	if err := st.CreateSecretStore(ctx, ss); err != nil {
		t.Fatal(err)
	}
	job := store.Job{
		EnvSecretRefs: []store.EnvSecretRef{
			{Name: "API_KEY", StoreID: ss.ID, Path: "jobs/foo", Key: "api_key"},
		},
	}
	if err := Resolve(ctx, st, &job); err == nil {
		t.Fatal("expected resolve error")
	}
}
