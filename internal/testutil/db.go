// Package testutil holds shared test helpers.
package testutil

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zypher-systems/kairos/internal/server/database"
	"github.com/zypher-systems/kairos/internal/server/store"
)

var (
	testMu   sync.Mutex
	testPool *pgxpool.Pool
)

const testSecret = "kairos-test-secret-not-for-production-32b"

// Store returns a Store pointed at KAIROS_TEST_DB_URL, or skips the test.
// Tests share one migrated database and run under a mutex with a truncate
// between each caller.
func Store(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("KAIROS_TEST_DB_URL")
	if dsn == "" {
		t.Skip("KAIROS_TEST_DB_URL unset")
	}
	testMu.Lock()
	t.Cleanup(func() {
		truncate(t, testPool)
		testMu.Unlock()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if testPool == nil {
		pool, err := database.Connect(ctx, dsn, 0)
		if err != nil {
			t.Skipf("test db unavailable: %v", err)
		}
		testPool = pool
	}
	truncate(t, testPool)
	return store.New(testPool, testSecret)
}

func truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `
		TRUNCATE
			notify_outbox,
			run_logs, queue_claims, run_cancel_signals, runs,
			workflow_edges, workflow_executions, workflows,
			jobs, files, agents, ssh_hosts, ssh_keys, notifications, secret_stores, storage_locations, calendars, api_tokens,
			sessions, admins, audit_events, ha_leader
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO ha_leader (name, holder_id, lease_expires_at)
		VALUES ('scheduler', '', to_timestamp(0))
		ON CONFLICT (name) DO UPDATE
		SET holder_id = '', lease_expires_at = to_timestamp(0), updated_at = now()`)
	if err != nil {
		t.Fatalf("reseed ha_leader: %v", err)
	}
}
