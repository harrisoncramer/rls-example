package rls

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX matches the interface that SQLC and pgxpool both satisfy.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// AcquireAsAppUser gets a connection from the pool and switches to the app_user
// role. The postgres superuser bypasses RLS even with FORCE ROW LEVEL SECURITY,
// so we need a non-superuser role to actually exercise RLS policies. The
// connection is cleaned up automatically when the test ends.
func AcquireAsAppUser(t *testing.T, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	if _, err := conn.Exec(ctx, "SET ROLE app_user"); err != nil {
		conn.Release()
		return nil, fmt.Errorf("failed to set role app_user: %w", err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(ctx, "RESET ROLE"); err != nil {
			t.Errorf("failed to reset role: %v", err)
		}
		conn.Release()
	})
	return conn, nil
}

// AcquireAsAdmin gets a connection from the pool and switches to the app_system
// role (BYPASSRLS). Used for admin operations, cross-tenant queries, seeding,
// and background jobs that span organizations.
func AcquireAsAdmin(t *testing.T, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	if _, err := conn.Exec(ctx, "SET ROLE app_system"); err != nil {
		conn.Release()
		return nil, fmt.Errorf("failed to set role app_system: %w", err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(ctx, "RESET ROLE"); err != nil {
			t.Errorf("failed to reset role: %v", err)
		}
		conn.Release()
	})
	return conn, nil
}

// SetOrg sets the app.current_org session variable on a connection. This is
// what production middleware does on every request after resolving the caller's
// organization from their auth token.
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
