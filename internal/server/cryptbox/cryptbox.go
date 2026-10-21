// Package cryptbox provides authenticated encryption for secrets at rest
// (SSH keys, agent tokens, job env secrets). The key is derived from the
// server master secret (KAIROS_SECRET); a random 12-byte nonce is stored
// with each ciphertext.
package cryptbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Seal encrypts plaintext under the master secret. Output is base64.
func Seal(masterSecret string, plaintext []byte) (string, error) {
	key := deriveKey(masterSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Open decrypts a Seal output. Returns an error for tampered or
// wrong-master-secret ciphertexts.
func Open(masterSecret, sealed string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sealed))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	key := deriveKey(masterSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// Box holds a bound master secret for convenient sealing/opening.
type Box struct {
	Secret string
}

// Seal encrypts plaintext.
func (b Box) Seal(plaintext []byte) (string, error) { return Seal(b.Secret, plaintext) }

// Open decrypts ciphertext.
func (b Box) Open(sealed string) ([]byte, error) { return Open(b.Secret, sealed) }

// deriveKey stretches the master secret to an AES-256 key.
func deriveKey(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}
