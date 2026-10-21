package sshx

import "testing"

func TestPinHostKeyTOFUAndMismatch(t *testing.T) {
	got, err := pinHostKey("", "SHA256:abc")
	if err != nil || got != "SHA256:abc" {
		t.Fatalf("empty stored must remember seen: %q %v", got, err)
	}
	got, err = pinHostKey("SHA256:abc", "SHA256:abc")
	if err != nil || got != "SHA256:abc" {
		t.Fatalf("matching pin must pass: %q %v", got, err)
	}
	if _, err := pinHostKey("SHA256:abc", "SHA256:evil"); err == nil {
		t.Fatal("mismatch must fail even without VerifyHostKey")
	}
}
