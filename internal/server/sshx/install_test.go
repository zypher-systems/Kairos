package sshx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strconv"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

// fakeSSH accepts connections and records whether a password was offered.
func fakeSSH(t *testing.T) (host string, port int, fp string, sawPassword *atomic.Bool) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sawPassword = new(atomic.Bool)
	cfg := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
		sawPassword.Store(true)
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					_ = ch.Reject(ssh.Prohibited, "test")
				}
				sc.Close()
			}()
		}
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ = strconv.Atoi(p)
	return h, port, FingerprintSHA256(signer.PublicKey()), sawPassword
}

// A host whose key does not match the pin never receives the password.
func TestInstallPublicKeyRefusesWrongHostKeyBeforePassword(t *testing.T) {
	host, port, _, saw := fakeSSH(t)
	_, err := InstallPublicKey(context.Background(), host, port, "root", "hunter2", "ssh-ed25519 AAAA test", "SHA256:not-this-host")
	if err == nil {
		t.Fatal("expected host key mismatch")
	}
	if saw.Load() {
		t.Fatal("password was sent to a host with the wrong key")
	}
}

// With the right pin the connection proceeds to authentication.
func TestInstallPublicKeyUsesPinnedKey(t *testing.T) {
	host, port, fp, saw := fakeSSH(t)
	_, _ = InstallPublicKey(context.Background(), host, port, "root", "hunter2", "ssh-ed25519 AAAA test", fp)
	if !saw.Load() {
		t.Fatal("matching host key should reach password auth")
	}
}
