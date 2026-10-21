package notify

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckHTTPURLRejectsLoopback(t *testing.T) {
	if err := CheckHTTPURL("http://127.0.0.1/hook"); err == nil {
		t.Fatal("loopback must be rejected")
	}
	if err := CheckHTTPURL("http://localhost/hook"); err == nil {
		t.Fatal("localhost must be rejected")
	}
	if err := CheckHTTPURL("http://10.0.0.5/hook"); err == nil {
		t.Fatal("RFC1918 must be rejected")
	}
	if err := CheckHTTPURL("file:///etc/passwd"); err == nil {
		t.Fatal("non-http must be rejected")
	}
}

func TestCheckHTTPURLAllowlist(t *testing.T) {
	t.Setenv("KAIROS_NOTIFY_ALLOW_HOSTS", "localhost,hooks.example.test")
	if err := CheckHTTPURL("http://localhost/hook"); err != nil {
		t.Fatal(err)
	}
	if err := CheckDialTarget("hooks.example.test"); err != nil {
		t.Fatal(err)
	}
}

func TestBlockedIPSpecialRanges(t *testing.T) {
	for _, s := range []string{"0.1.2.3", "100.64.0.1", "198.18.0.1", "192.0.0.8", "64:ff9b::a9fe:a9fe", "::ffff:127.0.0.1", "169.254.169.254", "fd00::1"} {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
}

// The check happens at connect time, so a hostname that resolves to a
// blocked address is refused even if an earlier lookup looked fine.
func TestSafeClientRefusesBlockedAddressAtDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	u := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	_, err := newSafeHTTPClient(5 * time.Second).Get(u)
	if err == nil || !errors.Is(err, ErrSSRF) {
		t.Fatalf("dial to loopback via hostname must fail with ErrSSRF, got %v", err)
	}
}

func TestSafeClientDoesNotFollowRedirects(t *testing.T) {
	t.Setenv("KAIROS_NOTIFY_ALLOW_HOSTS", "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	resp, err := newSafeHTTPClient(5 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect was followed: %d", resp.StatusCode)
	}
}

func TestHeaderValueStripsNewlines(t *testing.T) {
	if got := headerValue("job\r\nBcc: attacker@example.com"); strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header injection kept: %q", got)
	}
}
