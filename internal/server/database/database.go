package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/zypher-systems/kairos/migrations"
)

// migrationLockID is the pg_advisory_lock key for schema migrations ("kairos-migrate").
const migrationLockID int64 = 0x6b6169726f73

// Connect creates a pgx pool, pings, and applies pending migrations.
// Connect opens the pool, applies migrations under an advisory lock and
// returns the pool. maxConns caps the pool; 0 keeps pgx's default.
func Connect(ctx context.Context, url string, maxConns int) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if maxConns > 0 {
		pcfg.MaxConns = int32(maxConns)
	}
	pcfg.MaxConnLifetime = time.Hour
	pcfg.MaxConnIdleTime = 5 * time.Minute
	pcfg.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// Several replicas may start together; a session advisory lock makes them
	// apply migrations one at a time instead of racing on the same DDL.
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("acquire migration connection: %w", err)
	}
	defer lockConn.Release()
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		pool.Close()
		return nil, fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		_, _ = lockConn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		pool.Close()
		return nil, fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Up(sqlDB, "."); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	return pool, nil
}
