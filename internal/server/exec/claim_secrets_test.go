package exec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/secrets"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestClaimLocalRunSeesStoreEnv(t *testing.T) {

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"api_key": "from-vault"}},
		})
	}))
	t.Cleanup(srv.Close)
	secrets.HTTPClient = srv.Client()
	t.Cleanup(func() { secrets.HTTPClient = nil })

	st := testutil.Store(t)
	ctx := context.Background()
	ss := &store.SecretStore{Name: "v", Address: srv.URL, Mount: "secret", Token: "t"}
	if err := st.CreateSecretStore(ctx, ss); err != nil {
		t.Fatal(err)
	}
	j, err := st.CreateJob(ctx, &store.Job{
		Name:              "store-job",
		ScheduleType:      "interval",
		IntervalSeconds:   3600,
		Timezone:          "UTC",
		TargetType:        "local",
		ExecMode:          "shell",
		ShellScript:       "true",
		TimeoutSeconds:    60,
		ConcurrencyPolicy: "allow",
		Enabled:           true,
		EnvSecretRefs: []store.EnvSecretRef{
			{Name: "API_KEY", StoreID: ss.ID, Path: "jobs/foo", Key: "api_key"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueWithPolicy(ctx, j, store.TriggerManual, time.Now()); err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(st, nil, nil, 1, time.Second, 30*time.Second, 5*time.Second, t.TempDir())
	_, job, _, ok, err := d.claim(ctx)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if job.EnvSecrets["API_KEY"] != "from-vault" {
		t.Fatalf("claimed env %+v", job.EnvSecrets)
	}
}
