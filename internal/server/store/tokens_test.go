package store_test

import (
	"context"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestAPITokenRoundTrip(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	admin, err := st.CreateAdmin(ctx, "e2e", "e2e-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.CreateAPIToken(ctx, admin.ID, "ci", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token == "" || tok.Prefix == "" {
		t.Fatalf("secret must be returned once: %+v", tok)
	}
	ident, err := st.APITokenAdmin(ctx, tok.Token)
	if err != nil {
		t.Fatal(err)
	}
	if ident.ID != admin.ID || ident.Username != "e2e" || ident.Role != store.RoleAdmin {
		t.Fatalf("resolved %+v", ident)
	}
	listed, err := st.ListAPITokens(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Token != "" {
		t.Fatalf("list must omit secrets: %+v", listed)
	}
	if err := st.DeleteAPIToken(ctx, admin.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.APITokenAdmin(ctx, tok.Token); err == nil {
		t.Fatal("revoked token must not resolve")
	}
}
