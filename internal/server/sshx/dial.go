// Package sshx manages outbound SSH sessions: host-key verification
// (incl. trusted-on-first-use), key/password auth, and remote command
// execution with multiplexed output streaming.
package sshx

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostConfig describes an outbound connection.
type HostConfig struct {
	Host               string
	Port               int
	Username           string
	PrivateKeyPEM      string
	Password           string
	HostKeyFingerprint string // SHA256:... (base64, no padding); "" = TOFU
	VerifyHostKey      bool
	Timeout            time.Duration
}

// FingerprintSHA256 formats a host key in OpenSSH "SHA256:..." style.
func FingerprintSHA256(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])
}

// Result of a run: exit status and the verified host fingerprint.
type Result struct {
	ExitCode    int
	Fingerprint string
}

// Dial connects with auth and host-key handling. With VerifyHostKey=false it
// still verifies against a stored fingerprint when one is provided; the
// trusted fingerprint is returned in Result for TOFU persistence.
func Dial(cfg HostConfig) (*ssh.Client, *Result, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	var auth ssh.AuthMethod
	if cfg.PrivateKeyPEM != "" {
		var signer ssh.Signer
		var err error
		// Try passwordless first; skip passphrase keys (encrypted by kairos
		// only if the operator stores the key unencrypted).
		signer, err = ssh.ParsePrivateKey([]byte(cfg.PrivateKeyPEM))
		if err != nil {
			// fall back: passphrase-less only
			return nil, nil, fmt.Errorf("parse private key: %w", err)
		}
		auth = ssh.PublicKeys(signer)
	} else if cfg.Password != "" {
		auth = ssh.Password(cfg.Password)
	} else {
		return nil, nil, errors.New("no ssh credentials configured")
	}

	res := &Result{Fingerprint: cfg.HostKeyFingerprint}
	hostKeyCallback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := FingerprintSHA256(key)
		remember, err := pinHostKey(cfg.HostKeyFingerprint, fp)
		if err != nil {
			return err
		}
		res.Fingerprint = remember
		return nil
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: hostKeyCallback,
		Timeout:         cfg.Timeout,
	})
	if err != nil {
		return nil, res, err
	}
	return client, res, nil
}

// pinHostKey always verifies a stored fingerprint (even when VerifyHostKey is
// false). An empty stored value is TOFU: we remember what we saw.
func pinHostKey(stored, seen string) (string, error) {
	if stored == "" {
		return seen, nil
	}
	if stored != seen {
		return stored, fmt.Errorf("host key mismatch: got %s want %s", seen, stored)
	}
	return stored, nil
}
