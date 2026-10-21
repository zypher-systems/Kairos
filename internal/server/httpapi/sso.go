package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/zypher-systems/kairos/internal/server/cryptbox"

	"github.com/zypher-systems/kairos/internal/server/oidcauth"
	"github.com/zypher-systems/kairos/internal/server/store"
)

const (
	ssoPendingTTL = 10 * time.Minute
	ssoCookieName = "kairos_sso"
	ssoCookiePath = "/api/v1/auth/sso"
)

// ssoFlow is the in-progress sign-in. It travels in an encrypted, HttpOnly
// cookie rather than server memory, so the callback can land on any
// replica, and it only completes in the browser that started it (the
// state in the query must match the one in that browser's cookie).
type ssoFlow struct {
	State    string    `json:"s"`
	Verifier string    `json:"v"`
	Nonce    string    `json:"n"`
	Expires  time.Time `json:"e"`
}

func (a *API) putSSOFlow(w http.ResponseWriter, f ssoFlow) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	sealed, err := cryptbox.Seal(a.cfg.Secret, raw)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     ssoCookieName,
		Value:    sealed,
		Path:     ssoCookiePath,
		MaxAge:   int(ssoPendingTTL.Seconds()),
		HttpOnly: true,
		Secure:   a.cfg.HTTPS,
		SameSite: http.SameSiteLaxMode, // sent on the IdP's top-level redirect back
	})
	return nil
}

// takeSSOFlow reads and clears the browser's pending sign-in.
func (a *API) takeSSOFlow(w http.ResponseWriter, r *http.Request, state string) (ssoFlow, bool) {
	http.SetCookie(w, &http.Cookie{Name: ssoCookieName, Value: "", Path: ssoCookiePath, MaxAge: -1, HttpOnly: true, Secure: a.cfg.HTTPS, SameSite: http.SameSiteLaxMode})
	c, err := r.Cookie(ssoCookieName)
	if err != nil || c.Value == "" {
		return ssoFlow{}, false
	}
	raw, err := cryptbox.Open(a.cfg.Secret, c.Value)
	if err != nil {
		return ssoFlow{}, false
	}
	var f ssoFlow
	if json.Unmarshal(raw, &f) != nil || time.Now().After(f.Expires) {
		return ssoFlow{}, false
	}
	if subtle.ConstantTimeCompare([]byte(f.State), []byte(state)) != 1 {
		return ssoFlow{}, false
	}
	return f, true
}

func (a *API) ssoConfig(r *http.Request) (oidcauth.Config, store.Settings, error) {
	pub, err := a.store.GetSettings(r.Context())
	if err != nil {
		return oidcauth.Config{}, pub, err
	}
	secret, err := a.store.RevealSSOClientSecret(r.Context())
	if err != nil {
		return oidcauth.Config{}, pub, err
	}
	cfg := oidcauth.Config{
		Issuer:       pub.SSOIssuer,
		ClientID:     pub.SSOClientID,
		ClientSecret: secret,
		RedirectURL:  pub.SSORedirectURL,
	}
	return cfg, pub, nil
}

// POST /api/v1/auth/sso/start
func (a *API) handleSSOStart(w http.ResponseWriter, r *http.Request) {
	if a.loginLimit != nil && !a.loginLimit.allow(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	cfg, pub, err := a.ssoConfig(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	if !pub.SSOConfigured() {
		writeErr(w, http.StatusBadRequest, "SSO is not configured")
		return
	}
	state, err := oidcauth.RandomURLToken(32)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sso start failed")
		return
	}
	nonce, err := oidcauth.RandomURLToken(16)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sso start failed")
		return
	}
	verifier, err := oidcauth.RandomURLToken(32)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sso start failed")
		return
	}
	if err := a.putSSOFlow(w, ssoFlow{State: state, Verifier: verifier, Nonce: nonce, Expires: time.Now().Add(ssoPendingTTL)}); err != nil {
		writeErr(w, http.StatusInternalServerError, "sso start failed")
		return
	}
	authURL, err := a.oidc.AuthCodeURL(cfg, state, nonce, oidcauth.S256Challenge(verifier))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": authURL})
}

// GET /api/v1/auth/sso/callback
func (a *API) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("error") != "" {
		redirectSSOError(w, r, "denied")
		return
	}
	state := q.Get("state")
	code := q.Get("code")
	if state == "" || code == "" {
		redirectSSOError(w, r, "denied")
		return
	}
	flow, ok := a.takeSSOFlow(w, r, state)
	if !ok {
		redirectSSOError(w, r, "denied")
		return
	}
	cfg, pub, err := a.ssoConfig(r)
	if err != nil || !pub.SSOConfigured() {
		redirectSSOError(w, r, "misconfigured")
		return
	}
	id, err := a.oidc.Redeem(r.Context(), cfg, code, flow.Verifier, flow.Nonce)
	if err != nil {
		redirectSSOError(w, r, "denied")
		return
	}
	user, err := a.store.GetUserByOIDCSubject(r.Context(), id.Subject)
	if errors.Is(err, store.ErrOIDCUnlinked) {
		a.auditLogin(r, "", "login.fail")
		redirectSSOError(w, r, "unlinked")
		return
	}
	if errors.Is(err, store.ErrAccountDisabled) {
		a.auditLogin(r, user.Username, "login.fail")
		redirectSSOError(w, r, "disabled")
		return
	}
	if err != nil {
		redirectSSOError(w, r, "denied")
		return
	}
	if !a.issueSession(w, r, user.ID) {
		return
	}
	a.auditLogin(r, user.Username, "login.ok")
	http.Redirect(w, r, "/", http.StatusFound)
}

func redirectSSOError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?sso_error="+url.QueryEscape(code), http.StatusFound)
}
