// Package rls provides helpers for PostgreSQL Row-Level Security with
// transaction-scoped tenant isolation. This follows the industry standard
// pattern used by Supabase, PostgREST, and Citus: every request wraps its
// queries in a transaction and uses SET LOCAL to set the tenant context.
// SET LOCAL is automatically discarded when the transaction commits or
// rolls back, so there's no risk of leaking tenant context between pool
// checkouts.
//
// The package provides two layers of defense:
//
//  1. Pool level (ConfigurePool): every new connection defaults to app_user
//     via AfterConnect. Even if a codepath skips the transaction helpers,
//     it can never run as superuser.
//
//  2. Transaction level (BeginScopedTx / BeginAdminTx): wraps queries in a
//     transaction with SET LOCAL for role and org context. The transaction
//     auto-clears everything on commit/rollback.
package rls

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX matches the interface that SQLC, pgxpool, and pgx.Tx all satisfy.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GetPool gets a pool where every connection defaults to the app_user
// role. This is the safety net: if a handler skips the RLS middleware entirely,
// it still runs as app_user with no org set, which means RLS denies all access
// (NULL doesn't match any UUID). Data leaks become "see nothing" bugs instead
// of "see everything" bugs.
//
// The admin middleware explicitly upgrades to app_system when needed.
func GetPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	ConfigurePool(config)

	var pool *pgxpool.Pool
	for range 30 {
		pool, err = pgxpool.NewWithConfig(ctx, config)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				return pool, nil
			}
			pool.Close()
		}
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("gave up connecting to database: %w", err)
}

// ConfigurePool sets up the AfterConnect hook on a pool config so that every
// new connection defaults to the app_user role. This is defense-in-depth: if
// a codepath bypasses the transaction helpers and uses the pool directly, it
// still runs as app_user with no org set, which sees nothing.
func ConfigurePool(config *pgxpool.Config) {
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET ROLE app_user"); err != nil {
			return fmt.Errorf("failed to set default role app_user: %w", err)
		}
		return nil
	}
}

// BeginScopedTx starts a transaction scoped to a specific organization. It
// uses SET LOCAL so the role and org context are automatically discarded when
// the transaction commits or rolls back. This is the standard pattern used by
// Supabase, PostgREST, and Citus for RLS with connection pooling.
func BeginScopedTx(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE app_user"); err != nil {
		tx.Rollback(ctx)
		return nil, fmt.Errorf("failed to set local role app_user: %w", err)
	}

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL app.current_org = '%s'", orgID.String())); err != nil {
		tx.Rollback(ctx)
		return nil, fmt.Errorf("failed to set local app.current_org: %w", err)
	}

	return tx, nil
}

// BeginAdminTx starts a transaction with the app_system role (BYPASSRLS).
// No org scoping — queries see all data across all tenants. Used for admin
// operations, cross-tenant background jobs, and data backfills.
func BeginAdminTx(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE app_system"); err != nil {
		tx.Rollback(ctx)
		return nil, fmt.Errorf("failed to set local role app_system: %w", err)
	}

	return tx, nil
}

// --- Test helpers (use SET instead of SET LOCAL for non-transactional tests) ---

// AcquireAsAppUser gets a connection from the pool and switches to the app_user
// role. Used in unit tests where you need a raw connection rather than a
// transaction. The connection is cleaned up automatically when the test ends.
func AcquireAsAppUser(t *testing.T, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	t.Helper()
	return acquireWithRole(t, pool, "app_user")
}

// AcquireAsAdmin gets a connection from the pool and switches to the app_system
// role (BYPASSRLS). Used in unit tests for admin operations.
func AcquireAsAdmin(t *testing.T, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	t.Helper()
	return acquireWithRole(t, pool, "app_system")
}

func acquireWithRole(t *testing.T, pool *pgxpool.Pool, role string) (*pgxpool.Conn, error) {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	if err := SetRole(ctx, conn, role); err != nil {
		conn.Release()
		return nil, err
	}
	t.Cleanup(func() {
		if err := ResetRole(ctx, conn); err != nil {
			t.Errorf("failed to reset role: %v", err)
		}
		conn.Release()
	})
	return conn, nil
}

// --- Low-level helpers for manual connection management ---

// SetRole sets the PostgreSQL role on a connection (session-level SET).
func SetRole(ctx context.Context, db DBTX, role string) error {
	_, err := db.Exec(ctx, fmt.Sprintf("SET ROLE %s", role))
	if err != nil {
		return fmt.Errorf("failed to set role %s: %w", role, err)
	}
	return nil
}

// ResetRole clears the PostgreSQL role back to the connection's default.
func ResetRole(ctx context.Context, db DBTX) error {
	_, err := db.Exec(ctx, "RESET ROLE")
	if err != nil {
		return fmt.Errorf("failed to reset role: %w", err)
	}
	return nil
}

// SetOrg sets the app.current_org session variable (session-level SET).
func SetOrg(ctx context.Context, db DBTX, orgID uuid.UUID) error {
	_, err := db.Exec(ctx, fmt.Sprintf("SET app.current_org = '%s'", orgID.String()))
	if err != nil {
		return fmt.Errorf("failed to set app.current_org: %w", err)
	}
	return nil
}

// ResetOrg clears the app.current_org session variable.
func ResetOrg(ctx context.Context, db DBTX) error {
	_, err := db.Exec(ctx, "RESET app.current_org")
	if err != nil {
		return fmt.Errorf("failed to reset app.current_org: %w", err)
	}
	return nil
}
