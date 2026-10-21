// Package oidcauth implements OIDC authorization-code + PKCE (RS256 ID tokens).
package oidcauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config is the OIDC client configuration stored in Kairos settings.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// Result is the verified identity from an ID token.
type Result struct {
	Subject string
}

// Provider builds an authorization URL and redeems a code.
type Provider interface {
	AuthCodeURL(cfg Config, state, nonce, challenge string) (string, error)
	Redeem(ctx context.Context, cfg Config, code, verifier, nonce string) (Result, error)
}

// Fake is a test double. AuthCodeURL returns AuthURL; Redeem returns Subject.
type Fake struct {
	AuthURL string
	Subject string
	Err     error
}

// AuthCodeURL returns AuthURL with the state attached.
func (f *Fake) AuthCodeURL(cfg Config, state, nonce, challenge string) (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	u := f.AuthURL
	if u == "" {
		u = "https://idp.example/authorize"
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "state=" + url.QueryEscape(state), nil
}

// Redeem returns the configured subject.
func (f *Fake) Redeem(ctx context.Context, cfg Config, code, verifier, nonce string) (Result, error) {
	if f.Err != nil {
		return Result{}, f.Err
	}
	if f.Subject == "" {
		return Result{}, errors.New("fake oidc: no subject")
	}
	return Result{Subject: f.Subject}, nil
}

// RandomURLToken returns n random bytes as unpadded base64url.
func RandomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// S256Challenge is BASE64URL(SHA256(verifier)).
func S256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type cachedDisc struct {
	d   discovery
	exp time.Time
}

// Live talks to a real OpenID Provider.
type Live struct {
	HTTP *http.Client
	mu   sync.Mutex
	disc map[string]cachedDisc
}

// NewLive returns a Live client with a 15s HTTP timeout.
func NewLive() *Live {
	return &Live{
		HTTP: &http.Client{Timeout: 15 * time.Second},
		disc: map[string]cachedDisc{},
	}
}

func (l *Live) client() *http.Client {
	if l.HTTP != nil {
		return l.HTTP
	}
	return http.DefaultClient
}

func (l *Live) discover(ctx context.Context, issuer string) (discovery, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return discovery{}, errors.New("issuer required")
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return discovery{}, errors.New("issuer must be an http(s) URL")
	}
	l.mu.Lock()
	if c, ok := l.disc[issuer]; ok && time.Now().Before(c.exp) {
		l.mu.Unlock()
		return c.d, nil
	}
	l.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return discovery{}, err
	}
	resp, err := l.client().Do(req)
	if err != nil {
		return discovery{}, fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return discovery{}, fmt.Errorf("oidc discovery: HTTP %d", resp.StatusCode)
	}
	var d discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return discovery{}, fmt.Errorf("oidc discovery json: %w", err)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return discovery{}, errors.New("oidc discovery missing authorization, token, or jwks")
	}
	if d.Issuer == "" {
		d.Issuer = issuer
	}
	l.mu.Lock()
	l.disc[issuer] = cachedDisc{d: d, exp: time.Now().Add(time.Hour)}
	l.mu.Unlock()
	return d, nil
}

// AuthCodeURL builds the IdP authorization URL.
func (l *Live) AuthCodeURL(cfg Config, state, nonce, challenge string) (string, error) {
	d, err := l.discover(context.Background(), cfg.Issuer)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", cfg.RedirectURL)
	q.Set("scope", "openid")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return d.AuthorizationEndpoint + "?" + q.Encode(), nil
}

type tokenResp struct {
	IDToken string `json:"id_token"`
	Error   string `json:"error"`
}

// Redeem exchanges the code and verifies the ID token (RS256, iss/aud/exp/nonce/sub).
func (l *Live) Redeem(ctx context.Context, cfg Config, code, verifier, nonce string) (Result, error) {
	d, err := l.discover(ctx, cfg.Issuer)
	if err != nil {
		return Result{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", cfg.RedirectURL)
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := l.client().Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("oidc token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, err
	}
	var tr tokenResp
	if err := json.Unmarshal(body, &tr); err != nil {
		return Result{}, fmt.Errorf("oidc token json: %w", err)
	}
	if tr.Error != "" || tr.IDToken == "" {
		if tr.Error == "" {
			tr.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return Result{}, fmt.Errorf("oidc token: %s", tr.Error)
	}
	sub, err := verifyIDToken(ctx, l.client(), d, cfg.ClientID, tr.IDToken, nonce)
	if err != nil {
		return Result{}, err
	}
	return Result{Subject: sub}, nil
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`
	Aud   aud    `json:"aud"`
	Exp   int64  `json:"exp"`
	Iat   int64  `json:"iat"`
	Nonce string `json:"nonce"`
}

type aud []string

func (a *aud) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = []string{s}
		return nil
	}
	var ss []string
	if err := json.Unmarshal(b, &ss); err != nil {
		return err
	}
	*a = ss
	return nil
}

func verifyIDToken(ctx context.Context, hc *http.Client, d discovery, clientID, raw, nonce string) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", errors.New("id_token is not a JWT")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("id_token header")
	}
	var hdr jwtHeader
	if err := json.Unmarshal(headerJSON, &hdr); err != nil {
		return "", errors.New("id_token header json")
	}
	if hdr.Alg != "RS256" {
		return "", fmt.Errorf("unsupported id_token alg %s", hdr.Alg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("id_token signature")
	}
	pub, err := fetchRSA(ctx, hc, d.JWKSURI, hdr.Kid)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return "", fmt.Errorf("id_token signature: %w", err)
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("id_token claims")
	}
	var c jwtClaims
	if err := json.Unmarshal(claimsJSON, &c); err != nil {
		return "", errors.New("id_token claims json")
	}
	wantIss := strings.TrimRight(d.Issuer, "/")
	gotIss := strings.TrimRight(c.Iss, "/")
	if gotIss != wantIss {
		return "", errors.New("id_token issuer mismatch")
	}
	okAud := false
	for _, a := range c.Aud {
		if a == clientID {
			okAud = true
			break
		}
	}
	if !okAud {
		return "", errors.New("id_token audience mismatch")
	}
	if c.Exp == 0 || time.Unix(c.Exp, 0).Before(time.Now().Add(-30*time.Second)) {
		return "", errors.New("id_token expired")
	}
	if nonce != "" && c.Nonce != nonce {
		return "", errors.New("id_token nonce mismatch")
	}
	if strings.TrimSpace(c.Sub) == "" {
		return "", errors.New("id_token missing sub")
	}
	return c.Sub, nil
}

func fetchRSA(ctx context.Context, hc *http.Client, jwksURI, kid string) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: HTTP %d", resp.StatusCode)
	}
	var set jwks
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("jwks json: %w", err)
	}
	var match *jwk
	for i := range set.Keys {
		k := &set.Keys[i]
		if k.Kty != "RSA" {
			continue
		}
		if kid == "" || k.Kid == kid {
			match = k
			if k.Kid == kid {
				break
			}
		}
	}
	if match == nil {
		return nil, errors.New("jwks: no matching RSA key")
	}
	nb, err := base64.RawURLEncoding.DecodeString(match.N)
	if err != nil {
		return nil, errors.New("jwks n")
	}
	eb, err := base64.RawURLEncoding.DecodeString(match.E)
	if err != nil {
		return nil, errors.New("jwks e")
	}
	e := 0
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, errors.New("jwks e zero")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
}
