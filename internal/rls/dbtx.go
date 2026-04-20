// Auto-scoped DBTX implementations that wrap each database operation in a
// transaction with SET LOCAL for role and org context. These satisfy the DBTX
// interface that SQLC's db.New() accepts, so handlers use the standard SQLC
// pattern:
//
//	queries := db.New(rls.Scoped(pool, orgID))
//	programs, err := queries.ListPrograms(ctx)
//
// Each Query/QueryRow/Exec call starts its own transaction with the correct
// SET LOCAL, executes the operation, and commits when the result is consumed.
// This keeps the transaction lifecycle invisible to the caller while
// maintaining the SET LOCAL safety guarantees.
//
// For operations that need multiple queries in a single transaction (atomicity),
// use WithScopedTx or WithAdminTx directly.
package rls

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/google/uuid"
)

// Scoped returns a DBTX that wraps each operation in a scoped transaction for
// the given org. Pass it to db.New() to get SQLC queries that are automatically
// tenant-isolated.
func Scoped(pool *pgxpool.Pool, orgID uuid.UUID) DBTX {
	return &txDBTX{
		beginTx: func(ctx context.Context) (pgx.Tx, error) {
			return BeginScopedTx(ctx, pool, orgID)
		},
	}
}

// Admin returns a DBTX that wraps each operation in an admin transaction
// (BYPASSRLS). Pass it to db.New() for cross-tenant operations.
func Admin(pool *pgxpool.Pool) DBTX {
	return &txDBTX{
		beginTx: func(ctx context.Context) (pgx.Tx, error) {
			return BeginAdminTx(ctx, pool)
		},
	}
}

// txDBTX satisfies the DBTX interface by wrapping each operation in a
// transaction. The beginTx function determines the transaction type
// (scoped or admin).
type txDBTX struct {
	beginTx func(ctx context.Context) (pgx.Tx, error)
}

func (d *txDBTX) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx, err := d.beginTx(ctx)
	if err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("failed to begin tx: %w", err)
	}
	result, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		tx.Rollback(ctx)
		return pgconn.CommandTag{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("failed to commit tx: %w", err)
	}
	return result, nil
}

func (d *txDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	tx, err := d.beginTx(ctx)
	if err != nil {
		return &errRow{err: fmt.Errorf("failed to begin tx: %w", err)}
	}
	return &txRow{row: tx.QueryRow(ctx, sql, args...), tx: tx, ctx: ctx}
}

func (d *txDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx, err := d.beginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin tx: %w", err)
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return &txRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

// txRow wraps pgx.Row and commits or rolls back the transaction when Scan
// is called. For INSERT...RETURNING queries, the commit finalizes the write.
// For SELECT queries, the commit is a no-op on the data side.
type txRow struct {
	row pgx.Row
	tx  pgx.Tx
	ctx context.Context
}

func (r *txRow) Scan(dest ...any) error {
	err := r.row.Scan(dest...)
	if err != nil {
		r.tx.Rollback(r.ctx)
		return err
	}
	if err := r.tx.Commit(r.ctx); err != nil {
		return fmt.Errorf("failed to commit tx: %w", err)
	}
	return nil
}

// errRow is returned when the transaction itself fails to start.
type errRow struct {
	err error
}

func (r *errRow) Scan(dest ...any) error {
	return r.err
}

// txRows wraps pgx.Rows and commits or rolls back the transaction when
// Close is called. SQLC-generated code always defers rows.Close(), so the
// transaction is finalized when the method returns.
type txRows struct {
	pgx.Rows
	tx  pgx.Tx
	ctx context.Context
}

func (r *txRows) Close() {
	r.Rows.Close()
	if r.Rows.Err() != nil {
		r.tx.Rollback(r.ctx)
	} else {
		r.tx.Commit(r.ctx)
	}
}
