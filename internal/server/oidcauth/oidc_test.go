package oidcauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLiveRedeemRS256(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	issuer = srv.URL

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "/authorize",
			"token_endpoint":         issuer + "/token",
			"jwks_uri":               issuer + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA", "alg": "RS256", "kid": "k1", "n": n, "e": e, "use": "sig",
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("code") != "good" || r.Form.Get("code_verifier") == "" {
			http.Error(w, "bad code", 400)
			return
		}
		now := time.Now().Unix()
		claims, _ := json.Marshal(map[string]any{
			"iss":   issuer,
			"sub":   "idp-user-1",
			"aud":   "kairos",
			"exp":   now + 300,
			"iat":   now,
			"nonce": "n-test",
		})
		hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
		h64 := base64.RawURLEncoding.EncodeToString(hdr)
		c64 := base64.RawURLEncoding.EncodeToString(claims)
		sum := sha256.Sum256([]byte(h64 + "." + c64))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			t.Error(err)
			http.Error(w, "sign", 500)
			return
		}
		tok := h64 + "." + c64 + "." + base64.RawURLEncoding.EncodeToString(sig)
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": tok})
	})

	live := NewLive()
	live.HTTP = srv.Client()
	cfg := Config{Issuer: issuer, ClientID: "kairos", ClientSecret: "s", RedirectURL: "http://kairos/cb"}
	url, err := live.AuthCodeURL(cfg, "st", "n-test", S256Challenge("verifier-aaaaaaaa"))
	if err != nil {
		t.Fatal(err)
	}
	if url == "" {
		t.Fatal("empty auth url")
	}
	got, err := live.Redeem(context.Background(), cfg, "good", "verifier-aaaaaaaa", "n-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "idp-user-1" {
		t.Fatalf("sub %q", got.Subject)
	}
}
