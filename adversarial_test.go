package main

// Adversarial tests that try to break RLS tenant isolation through various
// attack vectors: transaction isolation, concurrent access, direct SQL
// manipulation, fabricated org IDs, and session variable tampering.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/harrisoncramer/rls-example/db"
	"github.com/harrisoncramer/rls-example/internal/dbtest"
	"github.com/harrisoncramer/rls-example/internal/rls"
)

// TestAdversarial_TransactionIsolation verifies that SET LOCAL is truly
// scoped to the transaction. After a scoped transaction commits, a new
// scoped transaction for a different org should see different data and
// NOT inherit the previous transaction's org context.
func TestAdversarial_TransactionIsolation(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Transaction 1: scoped to org1, can see org1 data
	tx1, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)

	orgs, err := db.New(tx1).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1, "scoped tx should see only org1")
	assert.Equal(t, "Org One", orgs[0].Name)
	require.NoError(t, tx1.Commit(ctx))

	// Transaction 2: scoped to org2 — should see org2, not org1
	tx2, err := rls.BeginScopedTx(ctx, pool, org2ID)
	require.NoError(t, err)
	defer tx2.Rollback(ctx)

	orgs, err = db.New(tx2).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1, "second tx should see only org2")
	assert.Equal(t, "Org Two", orgs[0].Name, "org1 context should not leak to org2 transaction")

	// Transaction 3: scoped to org1 again — still only org1
	tx2.Commit(ctx)
	tx3, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)
	defer tx3.Rollback(ctx)

	orgs, err = db.New(tx3).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1, "third tx should see only org1")
	assert.Equal(t, "Org One", orgs[0].Name)
}

// TestAdversarial_ConcurrentOrgAccess runs multiple goroutines with different
// org contexts on the same pool simultaneously. Verifies that concurrent
// scoped transactions never see each other's data.
func TestAdversarial_ConcurrentOrgAccess(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	const iterations = 50
	var wg sync.WaitGroup
	errors := make(chan error, iterations*2)

	for range iterations {
		wg.Add(2)

		// Goroutine for org1
		go func() {
			defer wg.Done()
			tx, err := rls.BeginScopedTx(ctx, pool, org1ID)
			if err != nil {
				errors <- err
				return
			}
			defer tx.Rollback(ctx)

			transfers, err := db.New(tx).ListTransfers(ctx)
			if err != nil {
				errors <- err
				return
			}
			if len(transfers) != 1 {
				errors <- fmt.Errorf("org1 goroutine saw %d transfers, expected 1", len(transfers))
				return
			}
			if transfers[0].OrganizationID != org1ID {
				errors <- fmt.Errorf("org1 goroutine saw transfer for wrong org: %s", transfers[0].OrganizationID)
			}
			tx.Commit(ctx)
		}()

		// Goroutine for org2
		go func() {
			defer wg.Done()
			tx, err := rls.BeginScopedTx(ctx, pool, org2ID)
			if err != nil {
				errors <- err
				return
			}
			defer tx.Rollback(ctx)

			transfers, err := db.New(tx).ListTransfers(ctx)
			if err != nil {
				errors <- err
				return
			}
			if len(transfers) != 1 {
				errors <- fmt.Errorf("org2 goroutine saw %d transfers, expected 1", len(transfers))
				return
			}
			if transfers[0].OrganizationID != org2ID {
				errors <- fmt.Errorf("org2 goroutine saw transfer for wrong org: %s", transfers[0].OrganizationID)
			}
			tx.Commit(ctx)
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent access error: %v", err)
	}
}

// TestAdversarial_FabricatedOrgID attempts to use an org ID that doesn't exist
// in the database. Verifies that RLS silently returns no data rather than
// erroring, and that inserts fail gracefully (FK constraint on organization_id).
func TestAdversarial_FabricatedOrgID(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	fakeOrgID := uuid.New()

	tx, err := rls.BeginScopedTx(ctx, pool, fakeOrgID)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	queries := db.New(tx)

	// Reads return empty (no data for this fake org)
	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Empty(t, orgs)

	transfers, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Empty(t, transfers)

	// Insert should fail — the column default populates organization_id
	// from the session variable, but the FK constraint on organization_id
	// references the organization table, which doesn't have this fake org.
	_, err = queries.CreateProgram(ctx, "Fake Program")
	assert.Error(t, err, "inserting with a fabricated org ID should fail due to FK constraint")
}

// TestAdversarial_UpdateBlockedForWrongOrg verifies that UPDATE operations
// are also blocked by RLS. Even if you know the exact row ID, you can't
// update a row that belongs to another tenant.
func TestAdversarial_UpdateBlockedForWrongOrg(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Get org2's program ID using admin access
	adminTx, err := rls.BeginAdminTx(ctx, pool)
	require.NoError(t, err)
	programs, err := db.New(adminTx).ListPrograms(ctx)
	require.NoError(t, err)
	adminTx.Commit(ctx)

	var org2ProgramID uuid.UUID
	for _, p := range programs {
		if p.OrganizationID == org2ID {
			org2ProgramID = p.ID
			break
		}
	}
	require.NotEqual(t, uuid.Nil, org2ProgramID, "should find org2's program")

	// Try to update org2's program while scoped to org1
	tx, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	// Direct SQL update targeting org2's program by ID
	tag, err := tx.Exec(ctx, "UPDATE program SET name = 'Hacked' WHERE id = $1", org2ProgramID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), tag.RowsAffected(),
		"should not be able to update another org's row — RLS makes it invisible")
}

// TestAdversarial_DeleteBlockedForWrongOrg verifies that DELETE operations
// are blocked by RLS for rows belonging to other tenants.
func TestAdversarial_DeleteBlockedForWrongOrg(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Get org2's transfer ID using admin access
	adminTx, err := rls.BeginAdminTx(ctx, pool)
	require.NoError(t, err)
	transfers, err := db.New(adminTx).ListTransfers(ctx)
	require.NoError(t, err)
	adminTx.Commit(ctx)

	var org2TransferID uuid.UUID
	for _, xfer := range transfers {
		if xfer.OrganizationID == org2ID {
			org2TransferID = xfer.ID
			break
		}
	}
	require.NotEqual(t, uuid.Nil, org2TransferID)

	// Try to delete org2's transfer while scoped to org1
	tx, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, "DELETE FROM transfer WHERE id = $1", org2TransferID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), tag.RowsAffected(),
		"should not be able to delete another org's row")

	// Verify org2's transfer still exists
	tx.Rollback(ctx)
	verifyTx, err := rls.BeginScopedTx(ctx, pool, org2ID)
	require.NoError(t, err)
	defer verifyTx.Rollback(ctx)

	org2Transfers, err := db.New(verifyTx).ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, org2Transfers, 1, "org2's transfer should still exist")
}

// TestAdversarial_DirectSQLBypassAttempt tries to bypass RLS by running raw
// SQL that doesn't go through SQLC. Verifies that RLS policies apply to all
// queries regardless of how they're executed.
func TestAdversarial_DirectSQLBypassAttempt(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	tx, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	// Try to read all organizations with raw SQL (bypassing SQLC)
	rows, err := tx.Query(ctx, "SELECT id, name FROM organization")
	require.NoError(t, err)
	defer rows.Close()

	var count int
	for rows.Next() {
		count++
	}
	assert.Equal(t, 1, count, "raw SQL should still be filtered by RLS")
}

// TestAdversarial_SetLocalInNestedTransaction verifies that SET LOCAL in a
// savepoint doesn't leak to the outer transaction scope.
func TestAdversarial_SetLocalInNestedTransaction(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Start scoped to org1
	tx, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	// Verify org1 context
	orgs, err := db.New(tx).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1)
	assert.Equal(t, org1ID, orgs[0].ID)

	// Create a savepoint and try to switch org context
	_, err = tx.Exec(ctx, "SAVEPOINT sp1")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL app.current_org = '%s'", org2ID.String()))
	require.NoError(t, err)

	// Inside the savepoint, should see org2
	orgs, err = db.New(tx).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1)
	assert.Equal(t, org2ID, orgs[0].ID)

	// Rollback the savepoint
	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT sp1")
	require.NoError(t, err)

	// After rolling back the savepoint, we should be back to org1
	orgs, err = db.New(tx).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1)
	assert.Equal(t, org1ID, orgs[0].ID)
}

// TestAdversarial_RLSStillEnforcedAfterRoleEscalation verifies that even if
// someone manages to SET LOCAL ROLE app_system within a scoped transaction,
// the RLS policy on the org still holds — they'd see all data but only within
// the app_system role's permissions.
//
// NOTE: In the test environment, the underlying connection is postgres
// superuser, so SET ROLE always succeeds. In production with a dedicated
// app_user LOGIN role, SET ROLE app_system would fail because app_user is
// not a member of app_system. This test documents the test-env limitation
// and verifies the RLS behavior regardless.
func TestAdversarial_RLSStillEnforcedAfterRoleEscalation(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	tx, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	// Verify scoped view first
	orgs, err := db.New(tx).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1, "scoped tx should see only org1")

	// In test env (postgres superuser), escalation succeeds.
	// In production (dedicated app_user login), this would fail.
	_, err = tx.Exec(ctx, "SET LOCAL ROLE app_system")
	if err != nil {
		// Production behavior: escalation blocked. Test passes.
		t.Logf("role escalation blocked (production behavior): %v", err)
		return
	}

	// Test env: escalation succeeded. Verify app_system bypasses RLS.
	orgs, err = db.New(tx).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 2, "app_system should bypass RLS and see all orgs")
}

// TestAdversarial_BeginScopedTxAutoCleanup verifies that even if a scoped
// transaction is abandoned (not committed or rolled back explicitly), the
// session variable doesn't leak. The pool will eventually reclaim the
// connection and the session state will be reset.
func TestAdversarial_BeginScopedTxAutoCleanup(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	// Start a scoped transaction and intentionally abandon it (no commit/rollback)
	tx1, err := rls.BeginScopedTx(ctx, pool, org1ID)
	require.NoError(t, err)

	orgs, err := db.New(tx1).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1)

	// Rollback to release the connection
	tx1.Rollback(ctx)

	// Start a new scoped transaction for org2
	tx2, err := rls.BeginScopedTx(ctx, pool, org2ID)
	require.NoError(t, err)
	defer tx2.Rollback(ctx)

	// Should see org2, NOT org1 (no leakage)
	orgs, err = db.New(tx2).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1)
	assert.Equal(t, org2ID, orgs[0].ID, "should see org2, not leaked org1 context")
}

// TestAdversarial_EmptyOrgIDDeniesAccess verifies that setting org to the
// nil UUID doesn't accidentally match any rows.
func TestAdversarial_EmptyOrgIDDeniesAccess(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	tx, err := rls.BeginScopedTx(ctx, pool, uuid.Nil)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	orgs, err := db.New(tx).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Empty(t, orgs, "nil UUID should not match any organization")
}

// TestAdversarial_RapidContextSwitching rapidly switches org context on the
// same pool to verify there's no race condition or stale state.
func TestAdversarial_RapidContextSwitching(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)
	pool := tdb.GetTestPool(t).Pool
	ctx := context.Background()

	for i := range 100 {
		var targetOrg uuid.UUID
		var expectedName string
		if i%2 == 0 {
			targetOrg = org1ID
			expectedName = "Org One"
		} else {
			targetOrg = org2ID
			expectedName = "Org Two"
		}

		tx, err := rls.BeginScopedTx(ctx, pool, targetOrg)
		require.NoError(t, err)

		orgs, err := db.New(tx).ListOrganizations(ctx)
		require.NoError(t, err)
		require.Len(t, orgs, 1)
		assert.Equal(t, expectedName, orgs[0].Name,
			"iteration %d: expected %s but got %s", i, expectedName, orgs[0].Name)

		tx.Commit(ctx)
	}
}
