package store

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hash, "t=3") {
		t.Fatalf("new hashes must encode t=3, got %s", hash)
	}
	if !VerifyPassword(hash, "correct-horse") {
		t.Fatal("same password must verify")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password must not verify")
	}
	if VerifyPassword("not-a-hash", "correct-horse") {
		t.Fatal("malformed hash must not verify")
	}
}

func TestVerifyPasswordLegacyTimeCost(t *testing.T) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	key := argon2.IDKey([]byte("correct-horse"), salt, 1, 64*1024, 4, 32)
	hash := "$argon2id$v=19$m=65536,t=1,p=4$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key)
	if !VerifyPassword(hash, "correct-horse") {
		t.Fatal("existing t=1 hashes must still verify")
	}
}
