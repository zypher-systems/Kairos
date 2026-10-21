package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// Admin is an account. The table is still named admins; the API calls them users.
type Admin struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	Role        string    `json:"role"`
	Team        string    `json:"team"`
	Enabled     bool      `json:"enabled"`
	OIDCSubject string    `json:"oidc_subject"`
	CreatedAt   time.Time `json:"created_at"`
}

// Identity is a resolved session or API token.
type Identity struct {
	ID       uuid.UUID
	Username string
	Role     string
	// ViaToken is set when the caller authenticated with an API token rather
	// than a session. Tokens cannot mint tokens.
	ViaToken bool
}

// RoleRank is 0 viewer, 1 operator, 2 admin. Unknown roles rank as viewer.
func RoleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 2
	case RoleOperator:
		return 1
	default:
		return 0
	}
}

// NormalizeRole accepts admin/operator/viewer. Empty becomes operator for new users.
func NormalizeRole(role string) (string, error) {
	switch role {
	case "", RoleOperator:
		return RoleOperator, nil
	case RoleAdmin, RoleViewer:
		return role, nil
	default:
		return "", fmt.Errorf("role must be admin, operator, or viewer")
	}
}

// MinRole returns the weaker of two roles.
func MinRole(a, b string) string {
	if RoleRank(a) <= RoleRank(b) {
		return a
	}
	return b
}

// argon2 parameters
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

// HashPassword derives an argon2id hash (PHC-like encoding).
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compares a password against an argon2id hash.
func VerifyPassword(hash, password string) bool {
	saltB64, keyB64, timeCost, memory, threads, ok := parseArgonHash(hash)
	if !ok {
		return false
	}
	salt, _ := base64.RawStdEncoding.DecodeString(saltB64)
	want, _ := base64.RawStdEncoding.DecodeString(keyB64)
	if len(salt) == 0 || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func parseArgonHash(hash string) (salt, key string, timeCost, memory uint32, threads uint8, ok bool) {
	// $argon2id$v=19$m=65536,t=3,p=4$salt$key
	parts := splitN(hash, '$', 6)
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return "", "", 0, 0, 0, false
	}
	timeCost, memory, threads = argonTime, argonMemory, argonThreads
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return "", "", 0, 0, 0, false
		}
		switch k {
		case "t":
			timeCost = uint32(n)
		case "m":
			memory = uint32(n)
		case "p":
			threads = uint8(n)
		}
	}
	return parts[4], parts[5], timeCost, memory, threads, true
}

func splitN(s string, sep byte, n int) []string {
	var out []string
	start := 0
	for i := 0; i < len(s) && len(out) < n-1; i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// CountAdmins reports the number of configured admin accounts.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admins`).Scan(&n)
	return n, err
}

// CreateAdmin creates the initial admin (only when none exists).
func (s *Store) CreateAdmin(ctx context.Context, username, password string) (Admin, error) {
	if len(password) < 8 {
		return Admin{}, errors.New("password must be at least 8 characters")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Admin{}, err
	}
	var a Admin
	err = s.pool.QueryRow(ctx, `
		INSERT INTO admins (username, password_hash, role, team, enabled)
		SELECT $1, $2, 'admin', '', true WHERE (SELECT count(*) FROM admins) = 0
		RETURNING id, username, role, team, enabled, oidc_subject, created_at`,
		username, hash,
	).Scan(&a.ID, &a.Username, &a.Role, &a.Team, &a.Enabled, &a.OIDCSubject, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Admin{}, errors.New("an admin account already exists")
	}
	return a, err
}

// GetAdminByUsername resolves an admin for login.
func (s *Store) GetAdminByUsername(ctx context.Context, username string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var hash string
	var enabled bool
	err := s.pool.QueryRow(ctx, `
		SELECT id, password_hash, enabled FROM admins WHERE username = $1`, username).Scan(&id, &hash, &enabled)
	if err != nil {
		return uuid.Nil, "", err
	}
	if !enabled {
		return uuid.Nil, "", ErrAccountDisabled
	}
	return id, hash, nil
}

// GetAdminPasswordHash returns the hash for an enabled or disabled account (password change).
func (s *Store) GetAdminPasswordHash(ctx context.Context, username string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT id, password_hash FROM admins WHERE username = $1`, username).Scan(&id, &hash)
	return id, hash, err
}

// NewSessionToken returns a fresh opaque session token.
func NewSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashSessionToken derives the stored token hash.
func HashSessionToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Session lifetime.
const SessionTTL = 7 * 24 * time.Hour

// CreateSession stores a session and returns the token.
func (s *Store) CreateSession(ctx context.Context, adminID uuid.UUID, userAgent, ip string) (string, time.Time, error) {
	token, err := NewSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(SessionTTL)
	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions (admin_id, token_hash, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		adminID, HashSessionToken(token), userAgent, ip, expires,
	)
	return token, expires, err
}

// SessionAdmin validates a session token, slides expiry, and records activity.
func (s *Store) SessionAdmin(ctx context.Context, token string) (Identity, error) {
	hash := HashSessionToken(token)
	var ident Identity
	var expires time.Time
	var enabled bool
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.username, a.role, a.enabled, se.expires_at
		FROM sessions se JOIN admins a ON a.id = se.admin_id
		WHERE se.token_hash = $1 AND se.expires_at > now()`,
		hash,
	).Scan(&ident.ID, &ident.Username, &ident.Role, &enabled, &expires)
	if err != nil {
		return Identity{}, err
	}
	if !enabled {
		return Identity{}, ErrAccountDisabled
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE sessions SET last_seen = now(), expires_at = now() + $2::interval
		WHERE token_hash = $1`, hash, SessionTTL.String())
	return ident, nil
}

// DeleteSession invalidates the session on logout.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, HashSessionToken(token))
	return err
}

// UpdateAdminPassword changes the admin password.
func (s *Store) UpdateAdminPassword(ctx context.Context, adminID uuid.UUID, newPassword string) error {
	if len(newPassword) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE admins SET password_hash = $2, updated_at = now() WHERE id = $1`, adminID, hash)
	return err
}
