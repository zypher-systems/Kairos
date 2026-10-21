package sshx

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// InstallPublicKey connects with password auth and appends a public key to
// the remote user's authorized_keys (idempotent: skips if already present).
// Used by the "push key to host" flow so a host can be switched to
// key-only auth afterwards.
//
// The host's pinned key is checked before the password is sent, so a
// machine impersonating the host cannot capture it. With no pin yet the key
// seen is returned for the caller to store (TOFU).
func InstallPublicKey(ctx context.Context, host string, port int, username, password, publicKeyLine, hostKeyFingerprint string) (string, error) {
	client, res, err := Dial(HostConfig{
		Host:               host,
		Port:               port,
		Username:           username,
		Password:           password,
		HostKeyFingerprint: hostKeyFingerprint,
		VerifyHostKey:      true,
		Timeout:            15 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

	// Shell-quote the key line (the alphabet is safe, but be strict anyway).
	q := "'" + strings.ReplaceAll(publicKeyLine, "'", `'\''`) + "'"
	script := strings.Join([]string{
		`set -eu`,
		`d="$HOME/.ssh"`,
		`mkdir -p "$d" && chmod 700 "$d"`,
		`f="$d/authorized_keys"`,
		`touch "$f" && chmod 600 "$f"`,
		`grep -qxF ` + q + ` "$f" || echo ` + q + ` >> "$f"`,
		`echo kairos-key-installed`,
	}, "\n")

	if _, err = Run(ctx, client, Command{Script: script}, WriteSinkFunc(func(stream, data string) {})); err != nil {
		return "", err
	}
	return res.Fingerprint, nil
}
