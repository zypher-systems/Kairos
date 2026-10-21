package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestUsersRolesAndLastAdmin(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	if admin.Role != store.RoleAdmin || !admin.Enabled {
		t.Fatalf("bootstrap must be enabled admin: %+v", admin)
	}
	op, err := st.CreateUser(ctx, "ops", "ops-pass-123", store.RoleOperator, "night")
	if err != nil {
		t.Fatal(err)
	}
	if op.Role != store.RoleOperator || op.Team != "night" {
		t.Fatalf("operator: %+v", op)
	}
	if _, err := st.CreateUser(ctx, "ops", "other-pass-123", store.RoleViewer, ""); !errors.Is(err, store.ErrUsernameTaken) {
		t.Fatalf("duplicate username: %v", err)
	}
	if _, err := st.UpdateUser(ctx, admin.ID, store.RoleViewer, "", true, "", ""); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("demote last admin: %v", err)
	}
	if err := st.DeleteUser(ctx, admin.ID); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("delete last admin: %v", err)
	}
	_, err = st.CreateUser(ctx, "second-admin", "admin-pass-123", store.RoleAdmin, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOIDCSubjectLinkOnly(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.CreateUser(ctx, "ops", "ops-pass-123", store.RoleOperator, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.UpdateUser(ctx, admin.ID, store.RoleAdmin, "", true, "", "idp-root")
	if err != nil {
		t.Fatal(err)
	}
	if got.OIDCSubject != "idp-root" {
		t.Fatalf("link: %+v", got)
	}
	if _, err := st.UpdateUser(ctx, op.ID, store.RoleOperator, "", true, "", "idp-root"); !errors.Is(err, store.ErrOIDCSubjectTaken) {
		t.Fatalf("duplicate subject: %v", err)
	}
	linked, err := st.GetUserByOIDCSubject(ctx, "idp-root")
	if err != nil || linked.Username != "root" {
		t.Fatalf("lookup: %+v %v", linked, err)
	}
	if _, err := st.GetUserByOIDCSubject(ctx, "nobody"); !errors.Is(err, store.ErrOIDCUnlinked) {
		t.Fatalf("unlinked: %v", err)
	}
}

func TestTokenRoleCeiling(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	ceiling := store.RoleViewer
	tok, err := st.CreateAPIToken(ctx, admin.ID, "read", &ceiling, nil)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := st.APITokenAdmin(ctx, tok.Token)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Role != store.RoleViewer {
		t.Fatalf("ceiling must cap admin to viewer, got %s", ident.Role)
	}
}

func TestAuditAppendAndList(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	id := admin.ID
	if err := st.AppendAudit(ctx, store.AuditEvent{
		ActorID:       &id,
		ActorUsername: admin.Username,
		Action:        "job.create",
		ObjectType:    "job",
		ObjectID:      "abc",
		Detail:        "nightly",
		IP:            "127.0.0.1",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListAudit(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "job.create" || events[0].ActorUsername != "root" {
		t.Fatalf("events: %+v", events)
	}
}

func TestDisabledAccountRejected(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	admin, err := st.CreateAdmin(ctx, "root", "root-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateUser(ctx, "view", "view-pass-123", store.RoleAdmin, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateUser(ctx, admin.ID, store.RoleAdmin, "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetAdminByUsername(ctx, "root"); !errors.Is(err, store.ErrAccountDisabled) {
		t.Fatalf("disabled login: %v", err)
	}
}
