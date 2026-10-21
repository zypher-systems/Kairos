// Package store is the persistence layer: jobs, runs, logs, queue claims,
// settings, agents, SSH hosts — all backed by Postgres.
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zypher-systems/kairos/internal/server/cryptbox"
)

// Store holds the database pool and the at-rest crypto box.
type Store struct {
	pool *pgxpool.Pool
	box  *cryptbox.Box
}

// New wraps a pgx pool in a Store. The master secret enables at-rest
// encryption; pass "" to disable (dev only — secrets stored in plaintext).
func New(pool *pgxpool.Pool, masterSecret string) *Store {
	var box *cryptbox.Box
	if masterSecret != "" {
		box = &cryptbox.Box{Secret: masterSecret}
	}
	return &Store{pool: pool, box: box}
}

// Pool exposes the underlying pool for transactional work by other packages.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Begin starts a transaction.
func (s *Store) Begin(ctx context.Context) (pgx.Tx, error) {
	return s.pool.Begin(ctx)
}
