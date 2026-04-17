// Package workers provides example River workers that demonstrate RLS
// integration for both tenant-scoped and cross-tenant background jobs.
//
// Scoped workers (e.g. CreateTransferWorker) call rls.WithScopedTx to get
// a transaction scoped to the tenant from their job args. All queries run
// within this transaction are automatically filtered by RLS.
//
// Admin workers (e.g. ReconciliationWorker) call rls.WithAdminTx to get
// a transaction that bypasses RLS for cross-tenant operations like batch
// settlement or reconciliation.
package workers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/harrisoncramer/rls-example/db"
	"github.com/harrisoncramer/rls-example/internal/rls"
)

// --- Scoped worker: creates a transfer for a specific org ---

// CreateTransferArgs are the job arguments for creating a transfer.
// OrganizationID is used by the RLS middleware to scope the transaction.
type CreateTransferArgs struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	ProgramID      uuid.UUID `json:"program_id"`
	Amount         int32     `json:"amount"`
	Description    string    `json:"description"`
}

func (CreateTransferArgs) Kind() string { return "create_transfer" }

func (a CreateTransferArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "default", MaxAttempts: 5}
}

// CreateTransferWorker is a tenant-scoped worker. It uses rls.WithScopedTx
// to get a transaction scoped to the organization from job args. The worker
// never explicitly passes organization_id to queries — the column default
// populates it from the session variable set by the scoped transaction.
type CreateTransferWorker struct {
	pool *pgxpool.Pool
	river.WorkerDefaults[CreateTransferArgs]
}

// NewCreateTransferWorker creates a new scoped worker.
func NewCreateTransferWorker(pool *pgxpool.Pool) *CreateTransferWorker {
	return &CreateTransferWorker{pool: pool}
}

func (w *CreateTransferWorker) Work(ctx context.Context, job *river.Job[CreateTransferArgs]) error {
	return rls.WithScopedTx(ctx, w.pool, job.Args.OrganizationID, func(ctx context.Context, tx pgx.Tx) error {
		queries := db.New(tx)

		desc := job.Args.Description
		_, err := queries.CreateTransfer(ctx, &db.CreateTransferParams{
			ProgramID:   job.Args.ProgramID,
			Amount:      job.Args.Amount,
			Description: &desc,
		})
		if err != nil {
			return fmt.Errorf("failed to create transfer: %w", err)
		}

		return nil
	})
}

// --- Admin worker: reconciliation across all tenants ---

// ReconciliationArgs are the job arguments for a cross-tenant reconciliation.
// No organization_id — this job touches all tenants.
type ReconciliationArgs struct {
	BatchSize int `json:"batch_size"`
}

func (ReconciliationArgs) Kind() string { return "reconciliation" }

func (a ReconciliationArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "admin", MaxAttempts: 3}
}

// ReconciliationWorker is a cross-tenant worker. It uses rls.WithAdminTx
// to get a transaction that bypasses RLS entirely. This is for operations
// that need to see and modify data across all organizations, like batch
// settlement, reconciliation, or analytics aggregation.
type ReconciliationWorker struct {
	pool *pgxpool.Pool
	river.WorkerDefaults[ReconciliationArgs]
}

// NewReconciliationWorker creates a new admin worker.
func NewReconciliationWorker(pool *pgxpool.Pool) *ReconciliationWorker {
	return &ReconciliationWorker{pool: pool}
}

func (w *ReconciliationWorker) Work(ctx context.Context, job *river.Job[ReconciliationArgs]) error {
	return rls.WithAdminTx(ctx, w.pool, func(ctx context.Context, tx pgx.Tx) error {
		queries := db.New(tx)

		transfers, err := queries.ListTransfers(ctx)
		if err != nil {
			return fmt.Errorf("failed to list transfers: %w", err)
		}

		_ = transfers
		return nil
	})
}
