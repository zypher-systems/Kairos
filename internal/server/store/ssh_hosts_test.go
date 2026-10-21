package store_test

import (
	"context"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestUpdateSSHHostKeepsPinnedFingerprint(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	const pin = "SHA256:pinnedpinnedpinnedpinnedpinnedpinnedpinned"
	h := &store.SSHHost{Name: "web", Host: "10.0.0.5", Port: 22, Username: "root",
		AuthType: "password", HostKeyFingerprint: pin, VerifyHostKey: true}
	if err := st.CreateSSHHost(ctx, h, "", "pw"); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() string {
		t.Helper()
		got, err := st.GetSSHHost(ctx, h.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.HostKeyFingerprint
	}

	// The edit form never sends a fingerprint; the pin must survive.
	edit := store.SSHHost{ID: h.ID, Name: "web-renamed", Host: "10.0.0.5", Port: 22,
		Username: "deploy", AuthType: "password", VerifyHostKey: true}
	if err := st.UpdateSSHHost(ctx, &edit, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := fingerprint(); got != pin {
		t.Fatalf("rename/username edit dropped pin: %q", got)
	}

	// Pointing the entry at another host re-pins on next connect.
	edit.Host = "10.0.0.6"
	if err := st.UpdateSSHHost(ctx, &edit, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := fingerprint(); got != "" {
		t.Fatalf("host change kept old pin: %q", got)
	}

	// Explicit forget clears a pin.
	edit.HostKeyFingerprint = pin
	if err := st.UpdateSSHHost(ctx, &edit, "", ""); err != nil {
		t.Fatal(err)
	}
	edit.HostKeyFingerprint = ""
	edit.ForgetHostKey = true
	if err := st.UpdateSSHHost(ctx, &edit, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := fingerprint(); got != "" {
		t.Fatalf("forget kept pin: %q", got)
	}
}
