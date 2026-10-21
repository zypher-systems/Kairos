package store_test

import (
	"context"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestSecretStoreSealAndReveal(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ss := &store.SecretStore{
		Name:    "prod-vault",
		Type:    "vault",
		Address: "https://vault.example:8200",
		Mount:   "secret",
		Token:   "s.super-secret",
	}
	if err := st.CreateSecretStore(ctx, ss); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSecretStore(ctx, ss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token == "s.super-secret" {
		t.Fatal("stored token should be sealed, not plaintext")
	}
	store.MaskSecretStore(&got)
	if got.Token != store.SecretMask {
		t.Fatalf("mask %q", got.Token)
	}
	live, err := st.GetSecretStoreForDelivery(ctx, ss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Token != "s.super-secret" {
		t.Fatalf("revealed %q", live.Token)
	}
}

func TestJobSecretRefsRoundTrip(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	ss := &store.SecretStore{Name: "v", Type: "vault", Address: "http://127.0.0.1:8200", Mount: "secret", Token: "t"}
	if err := st.CreateSecretStore(ctx, ss); err != nil {
		t.Fatal(err)
	}
	j := localJob("with-ref")
	j.EnvSecretRefs = []store.EnvSecretRef{{Name: "API_KEY", StoreID: ss.ID, Path: "jobs/x", Key: "api_key"}}
	created, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.EnvSecretRefs) != 1 || created.EnvSecretRefs[0].Name != "API_KEY" {
		t.Fatalf("refs %+v", created.EnvSecretRefs)
	}
}
