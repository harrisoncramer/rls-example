package workers

// These tests verify that River workers correctly integrate with RLS.
// Scoped workers should only see/write data for their org. Admin workers
// should see all data. Both use Blueprint for isolated test databases.

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/harrisoncramer/rls-example/db"
	"github.com/harrisoncramer/rls-example/internal/dbtest"
	"github.com/harrisoncramer/rls-example/internal/rls"
)

var (
	org1ID = uuid.New()
	org2ID = uuid.New()
)

func seedTwoOrgs(ctx context.Context, pool *pgxpool.Pool) error {
	if err := rls.SetRole(ctx, pool, "app_system"); err != nil {
		return err
	}

	queries := db.New(pool)

	if _, err := queries.CreateOrganization(ctx, &db.CreateOrganizationParams{
		ID: org1ID, Name: "Org One",
	}); err != nil {
		return err
	}
	if _, err := queries.CreateOrganization(ctx, &db.CreateOrganizationParams{
		ID: org2ID, Name: "Org Two",
	}); err != nil {
		return err
	}

	if _, err := queries.AdminCreateProgram(ctx, &db.AdminCreateProgramParams{
		OrganizationID: org1ID, Name: "Program Alpha",
	}); err != nil {
		return err
	}
	if _, err := queries.AdminCreateProgram(ctx, &db.AdminCreateProgramParams{
		OrganizationID: org2ID, Name: "Program Beta",
	}); err != nil {
		return err
	}

	return rls.ResetRole(ctx, pool)
}

// TestScopedWorker_CreatesTransferForCorrectOrg verifies that a scoped worker
// creates a transfer that is correctly tagged with the org from job args,
// and that the org_id was auto-populated from the session variable (not
// passed explicitly).
func TestScopedWorker_CreatesTransferForCorrectOrg(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Get org1's program ID
	adminTx, err := rls.BeginAdminTx(ctx, pool)
	require.NoError(t, err)
	programs, err := db.New(adminTx).ListPrograms(ctx)
	require.NoError(t, err)
	adminTx.Commit(ctx)

	var org1ProgramID uuid.UUID
	for _, p := range programs {
		if p.OrganizationID == org1ID {
			org1ProgramID = p.ID
			break
		}
	}
	require.NotEqual(t, uuid.Nil, org1ProgramID)

	// Simulate the worker's Work method
	worker := NewCreateTransferWorker(pool)
	job := &river.Job[CreateTransferArgs]{
		JobRow: nil,
		Args: CreateTransferArgs{
			OrganizationID: org1ID,
			ProgramID:      org1ProgramID,
			Amount:         5000,
			Description:    "Worker-created transfer",
		},
	}

	require.NoError(t, worker.Work(ctx, job))

	// Verify the transfer was created for org1
	tx, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	transfers, err := db.New(tx).ListTransfers(ctx)
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	assert.Equal(t, org1ID, transfers[0].OrganizationID,
		"transfer should be tagged with org1 from job args")
	assert.Equal(t, int32(5000), transfers[0].Amount)
}

// TestScopedWorker_CannotSeeOtherOrgData verifies that a scoped worker
// running for org1 cannot see org2's data, even if org2 has data in the
// same tables.
func TestScopedWorker_CannotSeeOtherOrgData(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Seed a transfer for org2 via admin
	adminTx, err := rls.BeginAdminTx(ctx, pool)
	require.NoError(t, err)
	programs, err := db.New(adminTx).ListPrograms(ctx)
	require.NoError(t, err)
	var org2ProgramID uuid.UUID
	for _, p := range programs {
		if p.OrganizationID == org2ID {
			org2ProgramID = p.ID
			break
		}
	}
	desc := "Org2 transfer"
	_, err = db.New(adminTx).AdminCreateTransfer(ctx, &db.AdminCreateTransferParams{
		ID: uuid.New(), ProgramID: org2ProgramID, OrganizationID: org2ID,
		Amount: 9999, Description: &desc,
	})
	require.NoError(t, err)
	adminTx.Commit(ctx)

	// Run a scoped operation as org1 and verify it can't see org2's transfer
	err = rls.WithScopedTx(ctx, pool, org1ID, func(ctx context.Context, tx pgx.Tx) error {
		transfers, err := db.New(tx).ListTransfers(ctx)
		if err != nil {
			return err
		}
		assert.Empty(t, transfers, "org1 scoped tx should not see org2's transfer")
		return nil
	})
	require.NoError(t, err)
}

// TestAdminWorker_SeesAllOrgs verifies that an admin worker can see data
// across all tenants.
func TestAdminWorker_SeesAllOrgs(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Seed transfers for both orgs
	adminTx, err := rls.BeginAdminTx(ctx, pool)
	require.NoError(t, err)
	programs, err := db.New(adminTx).ListPrograms(ctx)
	require.NoError(t, err)

	for _, p := range programs {
		desc := "Transfer for " + p.Name
		_, err := db.New(adminTx).AdminCreateTransfer(ctx, &db.AdminCreateTransferParams{
			ID: uuid.New(), ProgramID: p.ID, OrganizationID: p.OrganizationID,
			Amount: 1000, Description: &desc,
		})
		require.NoError(t, err)
	}
	adminTx.Commit(ctx)

	// Simulate admin worker — should see all transfers
	worker := NewReconciliationWorker(pool)
	job := &river.Job[ReconciliationArgs]{
		JobRow: nil,
		Args:   ReconciliationArgs{BatchSize: 100},
	}

	require.NoError(t, worker.Work(ctx, job))

	// Verify admin tx sees everything
	err = rls.WithAdminTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		transfers, err := db.New(tx).ListTransfers(ctx)
		if err != nil {
			return err
		}
		assert.Len(t, transfers, 2, "admin should see transfers for both orgs")
		return nil
	})
	require.NoError(t, err)
}

// TestScopedWorker_TransactionRollsBackOnError verifies that if a scoped
// worker returns an error, the transaction is rolled back and no data
// is persisted.
func TestScopedWorker_TransactionRollsBackOnError(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Run a scoped operation that creates a transfer then errors
	err = rls.WithScopedTx(ctx, pool, org1ID, func(ctx context.Context, tx pgx.Tx) error {
		queries := db.New(tx)

		programs, err := queries.ListPrograms(ctx)
		if err != nil {
			return err
		}
		require.Len(t, programs, 1)

		desc := "Should be rolled back"
		_, err = queries.CreateTransfer(ctx, &db.CreateTransferParams{
			ProgramID:   programs[0].ID,
			Amount:      7777,
			Description: &desc,
		})
		if err != nil {
			return err
		}

		return fmt.Errorf("intentional error to trigger rollback")
	})
	require.Error(t, err)

	// Verify the transfer was NOT persisted
	verifyTx, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)
	defer verifyTx.Rollback(ctx)

	transfers, err := db.New(verifyTx).ListTransfers(ctx)
	require.NoError(t, err)
	assert.Empty(t, transfers, "transfer should have been rolled back")
}

// TestConcurrentScopedWorkers runs multiple scoped workers concurrently for
// different orgs and verifies they don't interfere with each other.
func TestConcurrentScopedWorkers(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Get program IDs
	adminTx, err := rls.BeginAdminTx(ctx, pool)
	require.NoError(t, err)
	programs, err := db.New(adminTx).ListPrograms(ctx)
	require.NoError(t, err)
	adminTx.Commit(ctx)

	programByOrg := make(map[uuid.UUID]uuid.UUID)
	for _, p := range programs {
		programByOrg[p.OrganizationID] = p.ID
	}

	// Run 20 concurrent scoped workers (10 for each org)
	errCh := make(chan error, 20)
	for i := range 20 {
		go func(i int) {
			var orgID uuid.UUID
			if i%2 == 0 {
				orgID = org1ID
			} else {
				orgID = org2ID
			}

			worker := NewCreateTransferWorker(pool)
			job := &river.Job[CreateTransferArgs]{
				JobRow: nil,
				Args: CreateTransferArgs{
					OrganizationID: orgID,
					ProgramID:      programByOrg[orgID],
					Amount:         int32(100 + i),
					Description:    "concurrent worker",
				},
			}
			errCh <- worker.Work(ctx, job)
		}(i)
	}

	for range 20 {
		require.NoError(t, <-errCh)
	}

	// Verify each org has 10 transfers
	for _, orgID := range []uuid.UUID{org1ID, org2ID} {
		tx, err := rls.BeginScopedTx(ctx, pool, orgID)
		require.NoError(t, err)
		transfers, err := db.New(tx).ListTransfers(ctx)
		require.NoError(t, err)
		tx.Commit(ctx)
		assert.Len(t, transfers, 10, "each org should have 10 transfers from concurrent workers")
	}
}
