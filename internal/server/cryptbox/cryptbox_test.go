package cryptbox

import (
	"bytes"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	secret := "test-master-secret"
	plain := []byte("ssh private key material --BEGIN OPENSSH--")
	sealed, err := Seal(secret, plain)
	if err != nil {
		t.Fatal(err)
	}
	if sealed == string(plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	out, err := Open(secret, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestOpenWrongSecret(t *testing.T) {
	sealed, err := Seal("right", []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open("wrong", sealed); err == nil {
		t.Fatal("expected failure with wrong master secret")
	}
}

func TestOpenTampered(t *testing.T) {
	sealed, err := Seal("secret", []byte("important data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open("secret", sealed[:len(sealed)-3]+"000"); err == nil {
		t.Fatal("expected failure on tampered ciphertext")
	}
}
