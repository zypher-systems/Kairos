package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CreateUser adds an account.
func (s *Store) CreateUser(ctx context.Context, username, password, role, team string) (Admin, error) {
	if strings.TrimSpace(username) == "" {
		return Admin{}, errors.New("username required")
	}
	role, err := NormalizeRole(role)
	if err != nil {
		return Admin{}, err
	}
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
		VALUES ($1, $2, $3, $4, true)
		RETURNING id, username, role, team, enabled, oidc_subject, created_at`,
		username, hash, role, team,
	).Scan(&a.ID, &a.Username, &a.Role, &a.Team, &a.Enabled, &a.OIDCSubject, &a.CreatedAt)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return Admin{}, ErrUsernameTaken
		}
		return Admin{}, err
	}
	return a, nil
}

// ListUsers returns every account.
func (s *Store) ListUsers(ctx context.Context) ([]Admin, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, username, role, team, enabled, oidc_subject, created_at
		FROM admins ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Admin
	for rows.Next() {
		var a Admin
		if err := rows.Scan(&a.ID, &a.Username, &a.Role, &a.Team, &a.Enabled, &a.OIDCSubject, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountRole reports how many enabled accounts hold a role.
func (s *Store) CountRole(ctx context.Context, role string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admins WHERE role = $1 AND enabled`, role).Scan(&n)
	return n, err
}

// UpdateUser patches role, team, enabled, and OIDC subject. Password is optional.
func (s *Store) UpdateUser(ctx context.Context, id uuid.UUID, role, team string, enabled bool, newPassword, oidcSubject string) (Admin, error) {
	role, err := NormalizeRole(role)
	if err != nil {
		return Admin{}, err
	}
	if role != RoleAdmin {
		n, err := s.CountRole(ctx, RoleAdmin)
		if err != nil {
			return Admin{}, err
		}
		var current string
		if err := s.pool.QueryRow(ctx, `SELECT role FROM admins WHERE id = $1`, id).Scan(&current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Admin{}, ErrUserNotFound
			}
			return Admin{}, err
		}
		if current == RoleAdmin && n <= 1 {
			return Admin{}, ErrLastAdmin
		}
	}
	if !enabled {
		n, err := s.CountRole(ctx, RoleAdmin)
		if err != nil {
			return Admin{}, err
		}
		var current string
		var currentlyEnabled bool
		if err := s.pool.QueryRow(ctx, `SELECT role, enabled FROM admins WHERE id = $1`, id).Scan(&current, &currentlyEnabled); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Admin{}, ErrUserNotFound
			}
			return Admin{}, err
		}
		if current == RoleAdmin && currentlyEnabled && n <= 1 {
			return Admin{}, ErrLastAdmin
		}
	}
	if newPassword != "" {
		if err := s.UpdateAdminPassword(ctx, id, newPassword); err != nil {
			return Admin{}, err
		}
	}
	var a Admin
	err = s.pool.QueryRow(ctx, `
		UPDATE admins SET role = $2, team = $3, enabled = $4, oidc_subject = $5, updated_at = now()
		WHERE id = $1
		RETURNING id, username, role, team, enabled, oidc_subject, created_at`,
		id, role, team, enabled, strings.TrimSpace(oidcSubject),
	).Scan(&a.ID, &a.Username, &a.Role, &a.Team, &a.Enabled, &a.OIDCSubject, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Admin{}, ErrUserNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Admin{}, ErrOIDCSubjectTaken
	}
	return a, err
}

// DeleteUser removes an account. Refuses to delete the last admin.
func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM admins WHERE id = $1`, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if role == RoleAdmin {
		n, err := s.CountRole(ctx, RoleAdmin)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM admins WHERE id = $1`, id)
	return err
}

// GetUserByOIDCSubject resolves a linked account. Empty subject never matches.
func (s *Store) GetUserByOIDCSubject(ctx context.Context, subject string) (Admin, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return Admin{}, ErrOIDCUnlinked
	}
	var a Admin
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, role, team, enabled, oidc_subject, created_at
		FROM admins WHERE oidc_subject = $1`, subject,
	).Scan(&a.ID, &a.Username, &a.Role, &a.Team, &a.Enabled, &a.OIDCSubject, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Admin{}, ErrOIDCUnlinked
	}
	if err != nil {
		return Admin{}, err
	}
	if !a.Enabled {
		return a, ErrAccountDisabled
	}
	return a, nil
}
