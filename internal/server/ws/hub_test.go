package ws

import (
	"net/http"
	"testing"
)

func TestSameHostOrigin(t *testing.T) {
	req := func(origin, host string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, "http://"+host+"/api/v1/ws", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}

	if !SameHostOrigin(req("", "kairos.local:8080"), "") {
		t.Fatal("missing Origin must be allowed")
	}
	if !SameHostOrigin(req("http://kairos.local:8080", "kairos.local:8080"), "") {
		t.Fatal("same host must be allowed")
	}
	if SameHostOrigin(req("https://evil.example", "kairos.local:8080"), "") {
		t.Fatal("foreign origin must be rejected")
	}
	if SameHostOrigin(req("::not a url", "kairos.local:8080"), "") {
		t.Fatal("malformed origin must be rejected")
	}
	if !SameHostOrigin(req("https://kairos.example.com", "127.0.0.1:8080"), "https://kairos.example.com") {
		t.Fatal("PUBLIC_URL origin behind proxy must be allowed")
	}
	if SameHostOrigin(req("https://evil.example", "127.0.0.1:8080"), "https://kairos.example.com") {
		t.Fatal("foreign origin must still be rejected when PUBLIC_URL is set")
	}
}
