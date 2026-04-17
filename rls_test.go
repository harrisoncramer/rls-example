package main

// This file tests PostgreSQL Row-Level Security (RLS) enforcement for a
// multi-tenant schema where some tables are indirectly scoped through foreign
// key chains (transfer -> program -> organization, ledger_entry -> transfer ->
// program -> organization).
//
// After the phased migration, transfer and ledger_entry have a denormalized
// organization_id column whose DEFAULT is current_setting('app.current_org', true)::uuid.
// This means services don't need to explicitly pass organization_id on inserts —
// the database populates it from the session variable that was set on connection checkout.
//
// Every test gets its own copy-on-write database (via Blueprint) with all six
// migrations applied, so we're always testing against the final state: columns
// added, roles created, defaults set, backfill done, NOT NULL enforced, RLS on.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

// seedTwoOrgs creates two fully isolated tenants with data at every level of
// the table hierarchy. It runs as app_system (the BYPASSRLS role) so RLS
// doesn't interfere with seeding, and uses the AdminCreate queries which
// explicitly accept organization_id rather than relying on the session variable
// default. This is intentional — seed data is inserted by an admin role, not
// through the normal request path.
//
// After seeding:
//   - Org One: 1 program, 1 transfer ($1000), 2 ledger entries (debit + credit)
//   - Org Two: 1 program, 1 transfer ($2000), 1 ledger entry (debit)
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

	prog1, err := queries.AdminCreateProgram(ctx, &db.AdminCreateProgramParams{
		OrganizationID: org1ID, Name: "Program Alpha",
	})
	if err != nil {
		return err
	}
	prog2, err := queries.AdminCreateProgram(ctx, &db.AdminCreateProgramParams{
		OrganizationID: org2ID, Name: "Program Beta",
	})
	if err != nil {
		return err
	}

	xfer1, err := queries.AdminCreateTransfer(ctx, &db.AdminCreateTransferParams{
		ID: uuid.New(), ProgramID: prog1.ID, OrganizationID: org1ID,
		Amount: 1000, Description: strPtr("Transfer One"),
	})
	if err != nil {
		return err
	}
	xfer2, err := queries.AdminCreateTransfer(ctx, &db.AdminCreateTransferParams{
		ID: uuid.New(), ProgramID: prog2.ID, OrganizationID: org2ID,
		Amount: 2000, Description: strPtr("Transfer Two"),
	})
	if err != nil {
		return err
	}

	if _, err := queries.AdminCreateLedgerEntry(ctx, &db.AdminCreateLedgerEntryParams{
		TransferID: xfer1.ID, OrganizationID: org1ID, Amount: 1000, EntryType: "debit",
	}); err != nil {
		return err
	}
	if _, err := queries.AdminCreateLedgerEntry(ctx, &db.AdminCreateLedgerEntryParams{
		TransferID: xfer1.ID, OrganizationID: org1ID, Amount: 1000, EntryType: "credit",
	}); err != nil {
		return err
	}
	if _, err := queries.AdminCreateLedgerEntry(ctx, &db.AdminCreateLedgerEntryParams{
		TransferID: xfer2.ID, OrganizationID: org2ID, Amount: 2000, EntryType: "debit",
	}); err != nil {
		return err
	}

	if err := rls.ResetRole(ctx, pool); err != nil {
		return err
	}
	return nil
}

// TestRLS_IsolatesOrganizations verifies that when app.current_org is set to
// org1, only org1's data is visible across ALL tables — including transfer and
// ledger_entry, which were originally indirectly scoped through FK chains.
// Before the migration, isolating these tables required joining through
// transfer -> program -> organization. Now, RLS on the denormalized
// organization_id handles it automatically.
func TestRLS_IsolatesOrganizations(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, rls.SetOrg(ctx, conn, org1ID))

	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 1)
	assert.Equal(t, "Org One", orgs[0].Name)

	programs, err := queries.ListPrograms(ctx)
	require.NoError(t, err)
	assert.Len(t, programs, 1)
	assert.Equal(t, "Program Alpha", programs[0].Name)

	transfers, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, transfers, 1)
	assert.Equal(t, "Transfer One", *transfers[0].Description)

	entries, err := queries.ListLedgerEntries(ctx)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

// TestRLS_SessionDefaultPopulatesOrgID is the key test for the migration
// approach. It proves that the column default (current_setting('app.current_org'))
// works: when we create a transfer and ledger entry through SQLC-generated code,
// we never pass organization_id — look at CreateTransferParams and
// CreateLedgerEntryParams, they don't even have an OrganizationID field. Yet
// the returned rows have organization_id correctly populated from the session
// variable.
//
// This is what makes the phased migration practical: we don't need to update
// every insert codepath in the application. We set the session variable once
// on connection checkout, set the column default, and every INSERT gets
// organization_id for free.
func TestRLS_SessionDefaultPopulatesOrgID(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, rls.SetOrg(ctx, conn, org1ID))

	programs, err := queries.ListPrograms(ctx)
	require.NoError(t, err)
	require.Len(t, programs, 1)

	// CreateTransferParams has: ProgramID, Amount, Description
	// Notice: no OrganizationID field. The column default handles it.
	transfer, err := queries.CreateTransfer(ctx, &db.CreateTransferParams{
		ProgramID:   programs[0].ID,
		Amount:      5000,
		Description: strPtr("Auto-populated org"),
	})
	require.NoError(t, err)
	assert.Equal(t, org1ID, transfer.OrganizationID,
		"organization_id should be auto-populated from session variable")

	// Same for ledger_entry — two hops away from organization in the
	// original schema, but the denormalized column + default means we
	// don't need to know about that chain at all.
	entry, err := queries.CreateLedgerEntry(ctx, &db.CreateLedgerEntryParams{
		TransferID: transfer.ID,
		Amount:     5000,
		EntryType:  "debit",
	})
	require.NoError(t, err)
	assert.Equal(t, org1ID, entry.OrganizationID,
		"organization_id should be auto-populated from session variable")
}

// TestRLS_SwitchingOrgContext proves that the session variable is mutable
// within a single connection. This matters for connection pooling: when a
// pool connection is reused for a different request (different tenant), we
// just SET app.current_org to the new value and all subsequent queries
// see the new tenant's data. The old tenant's data becomes invisible.
func TestRLS_SwitchingOrgContext(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, rls.SetOrg(ctx, conn, org1ID))
	transfers1, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, transfers1, 1)

	require.NoError(t, rls.SetOrg(ctx, conn, org2ID))
	transfers2, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, transfers2, 1)
	assert.Equal(t, "Transfer Two", *transfers2[0].Description)
}

// TestRLS_InsertBlockedForWrongOrg verifies that the INSERT policy prevents
// writing data tagged with a different organization_id than the session
// variable, even when the org_id is passed explicitly (bypassing the column
// default). This is the database-level safety net — a bug in the service
// code can't accidentally write cross-tenant data.
func TestRLS_InsertBlockedForWrongOrg(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, rls.SetOrg(ctx, conn, org1ID))

	// Use the admin query which explicitly passes organization_id.
	// Even though the column value is org2, the RLS INSERT policy
	// checks it against the session variable (org1) and rejects it.
	_, err = queries.AdminCreateProgram(ctx, &db.AdminCreateProgramParams{
		OrganizationID: org2ID,
		Name:           "Sneaky Program",
	})
	assert.Error(t, err, "inserting a row for a different org should fail")
}

// TestRLS_GetByIDReturnsNothingForWrongOrg demonstrates a subtle but
// important behavior: fetching a row by primary key returns pgx.ErrNoRows
// (not a permission error) if the row belongs to another tenant. From the
// caller's perspective, the row simply doesn't exist. This is by design —
// RLS filters rows at the scan level, so the query acts as if the row
// isn't in the table at all.
//
// This is the scenario the proposal doc calls out: today, a query like
// "SELECT * FROM transfer WHERE id = $1" returns the row regardless of
// who owns it. With RLS, it silently returns nothing. Services need to
// be aware that "not found" might mean "belongs to another tenant."
func TestRLS_GetByIDReturnsNothingForWrongOrg(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	// First, get org2's transfer ID while we have org2 context
	require.NoError(t, rls.SetOrg(ctx, conn, org2ID))
	transfers, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	org2TransferID := transfers[0].ID

	// Switch to org1, try to fetch org2's transfer by its exact ID
	require.NoError(t, rls.SetOrg(ctx, conn, org1ID))
	_, err = queries.GetTransfer(ctx, org2TransferID)
	assert.ErrorIs(t, err, pgx.ErrNoRows,
		"fetching another org's row by ID should return no rows")
}

// TestRLS_NoSessionVariableReturnsNothing verifies the default-deny behavior.
// If a service forgets to set app.current_org (or it gets cleared between pool
// checkouts), current_setting('app.current_org', true) returns NULL. NULL doesn't
// match any UUID, so every RLS policy evaluates to false and all queries return
// empty results. No data leaks, no errors — just silence.
//
// This is safer than erroring: a forgotten session variable means "see nothing"
// rather than "see everything" (which is what happens today without RLS).
func TestRLS_NoSessionVariableReturnsNothing(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	// Deliberately do NOT call SetOrg — simulates a misconfigured connection
	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Empty(t, orgs)

	transfers, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Empty(t, transfers)

	entries, err := queries.ListLedgerEntries(ctx)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestRLS_BypassRoleSeesEverything confirms that the app_system role (which has
// BYPASSRLS) can see all data across all tenants without setting a session
// variable. This role is used for:
//   - Prisma migrations and schema changes
//   - Cross-tenant background jobs (batch settlement, reconciliation)
//   - Data backfills
//   - Admin tooling and analytics queries
//
// In production, this role should be restricted to system processes only.
// River workers that span tenants would use this role; single-tenant workers
// would use app_user with the org set from job args.
func TestRLS_BypassRoleSeesEverything(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAdmin(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 2)

	programs, err := queries.ListPrograms(ctx)
	require.NoError(t, err)
	assert.Len(t, programs, 2)

	transfers, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, transfers, 2)

	entries, err := queries.ListLedgerEntries(ctx)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

func strPtr(s string) *string {
	return &s
}
