package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/store"
)

const (
	sessionCookie = "kairos_session"
)

type ctxKey string

const (
	ctxAdminIDKey  ctxKey = "adminID"
	ctxUsernameKey ctxKey = "username"
	ctxRoleKey     ctxKey = "role"
	ctxViaTokenKey ctxKey = "viaToken"
)

func identityFrom(r *http.Request) store.Identity {
	id, _ := r.Context().Value(ctxAdminIDKey).(uuid.UUID)
	username, _ := r.Context().Value(ctxUsernameKey).(string)
	role, _ := r.Context().Value(ctxRoleKey).(string)
	via, _ := r.Context().Value(ctxViaTokenKey).(bool)
	return store.Identity{ID: id, Username: username, Role: role, ViaToken: via}
}

func withIdentity(ctx context.Context, ident store.Identity) context.Context {
	ctx = context.WithValue(ctx, ctxAdminIDKey, ident.ID)
	ctx = context.WithValue(ctx, ctxUsernameKey, ident.Username)
	ctx = context.WithValue(ctx, ctxRoleKey, ident.Role)
	ctx = context.WithValue(ctx, ctxViaTokenKey, ident.ViaToken)
	return ctx
}

func (a *API) resolveToken(ctx context.Context, token string) (store.Identity, error) {
	ident, err := a.store.SessionAdmin(ctx, token)
	if err == nil {
		return ident, nil
	}
	return a.store.APITokenAdmin(ctx, token)
}

// AdminAuth requires a valid session cookie (or Bearer token for tooling).
func (a *API) AdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		cookie, err := r.Cookie(sessionCookie)
		if err == nil && cookie.Value != "" {
			token = cookie.Value
		} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		ident, err := a.resolveToken(r.Context(), token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "session expired")
			return
		}
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), ident)))
	})
}

// RequireRole rejects callers below min (viewer < operator < admin).
func (a *API) RequireRole(min string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := r.Context().Value(ctxRoleKey).(string)
			if store.RoleRank(role) < store.RoleRank(min) {
				writeErr(w, http.StatusForbidden, "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (a *API) audit(r *http.Request, action, objectType, objectID, detail string) {
	ident := identityFrom(r)
	var actorID *uuid.UUID
	if ident.ID != uuid.Nil {
		id := ident.ID
		actorID = &id
	}
	_ = a.store.AppendAudit(r.Context(), store.AuditEvent{
		ActorID:       actorID,
		ActorUsername: ident.Username,
		Action:        action,
		ObjectType:    objectType,
		ObjectID:      objectID,
		Detail:        detail,
		IP:            clientIP(r),
	})
}

func (a *API) auditLogin(r *http.Request, username, action string) {
	_ = a.store.AppendAudit(r.Context(), store.AuditEvent{
		ActorUsername: username,
		Action:        action,
		ObjectType:    "session",
		IP:            clientIP(r),
	})
}

// guardOrigin rejects cross-site mutating requests.
func (a *API) guardOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}
			site := strings.ToLower(r.Header.Get("Sec-Fetch-Site"))
			if site == "same-origin" || site == "same-site" || site == "none" {
				next.ServeHTTP(w, r)
				return
			}
			writeErr(w, http.StatusForbidden, "missing origin")
			return
		}
		if !a.cfg.OriginAllowed(origin, r.Host) {
			writeErr(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AuthStatus is the setup/state descriptor for the SPA.
type AuthStatus struct {
	SetupRequired bool   `json:"setup_required"`
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
	Role          string `json:"role,omitempty"`
	SSOAvailable  bool   `json:"sso_available"`
}

// authStatus computes setup + session state without side effects.
func (a *API) authStatus(r *http.Request) AuthStatus {
	n, err := a.store.CountAdmins(r.Context())
	setup := err == nil && n == 0
	status := AuthStatus{SetupRequired: setup}
	if cfg, err := a.store.GetSettings(r.Context()); err == nil {
		status.SSOAvailable = cfg.SSOConfigured()
	}
	token := ""
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		token = cookie.Value
	} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	}
	if token != "" {
		if ident, err := a.resolveToken(r.Context(), token); err == nil {
			status.Authenticated = true
			status.Username = ident.Username
			status.Role = ident.Role
		}
	}
	return status
}

// GET /api/v1/auth/status
func (a *API) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.authStatus(r))
}

// POST /api/v1/auth/setup — first-run admin creation.
func (a *API) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if in.Username == "" || len(in.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "username required and password >= 8 chars")
		return
	}
	admin, err := a.store.CreateAdmin(r.Context(), in.Username, in.Password)
	if err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	if !a.issueSession(w, r, admin.ID) {
		return
	}
	a.auditLogin(r, admin.Username, "login.ok")
	writeJSON(w, http.StatusCreated, map[string]any{"username": admin.Username, "role": admin.Role})
}

// POST /api/v1/auth/login
func (a *API) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if a.loginLimit != nil && !a.loginLimit.allow(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	adminID, hash, err := a.store.GetAdminByUsername(r.Context(), in.Username)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrAccountDisabled) || (err == nil && !store.VerifyPassword(hash, in.Password)) {
		a.auditLogin(r, in.Username, "login.fail")
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	if !a.issueSession(w, r, adminID) {
		return
	}
	a.auditLogin(r, in.Username, "login.ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/auth/logout
func (a *API) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		_ = a.store.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cfg.HTTPS,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/password — change own password (admin-auth'd).
func (a *API) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	adminID, ok := r.Context().Value(ctxAdminIDKey).(uuid.UUID)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no session")
		return
	}
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	username, _ := r.Context().Value(ctxUsernameKey).(string)
	_, hash, err := a.store.GetAdminPasswordHash(r.Context(), username)
	if err != nil || !store.VerifyPassword(hash, in.Current) {
		writeErr(w, http.StatusUnauthorized, "current password is wrong")
		return
	}
	if err := a.store.UpdateAdminPassword(r.Context(), adminID, in.New); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "user.password", "user", adminID.String(), username)
	w.WriteHeader(http.StatusNoContent)
}

// issueSession creates a session and sets the cookie. False means the
// error response was already written.
func (a *API) issueSession(w http.ResponseWriter, r *http.Request, adminID uuid.UUID) bool {
	token, _, err := a.store.CreateSession(r.Context(), adminID, r.UserAgent(), clientIP(r))
	if err != nil {
		slog.Error("create session", "err", err)
		writeErr(w, http.StatusInternalServerError, "session creation failed")
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(store.SessionTTL),
		HttpOnly: true,
		Secure:   a.cfg.HTTPS,
		SameSite: http.SameSiteLaxMode,
	})
	return true
}

func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
