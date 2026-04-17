package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/harrisoncramer/rls-example/db"
	"github.com/harrisoncramer/rls-example/internal/dbtest"
)

var (
	org1ID = uuid.New()
	org2ID = uuid.New()
)

func seedTwoOrgs(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `SET ROLE app_system`); err != nil {
		return err
	}

	if _, err := pool.Exec(ctx, `INSERT INTO organization (id, name) VALUES ($1, 'Org One')`, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization (id, name) VALUES ($1, 'Org Two')`, org2ID); err != nil {
		return err
	}

	prog1ID := uuid.New()
	prog2ID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO program (id, organization_id, name) VALUES ($1, $2, 'Program Alpha')`, prog1ID, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO program (id, organization_id, name) VALUES ($1, $2, 'Program Beta')`, prog2ID, org2ID); err != nil {
		return err
	}

	xfer1ID := uuid.New()
	xfer2ID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO transfer (id, program_id, organization_id, amount, description) VALUES ($1, $2, $3, 1000, 'Transfer One')`, xfer1ID, prog1ID, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO transfer (id, program_id, organization_id, amount, description) VALUES ($1, $2, $3, 2000, 'Transfer Two')`, xfer2ID, prog2ID, org2ID); err != nil {
		return err
	}

	if _, err := pool.Exec(ctx, `INSERT INTO ledger_entry (transfer_id, organization_id, amount, entry_type) VALUES ($1, $2, 1000, 'debit')`, xfer1ID, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_entry (transfer_id, organization_id, amount, entry_type) VALUES ($1, $2, 1000, 'credit')`, xfer1ID, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_entry (transfer_id, organization_id, amount, entry_type) VALUES ($1, $2, 2000, 'debit')`, xfer2ID, org2ID); err != nil {
		return err
	}

	if _, err := pool.Exec(ctx, `RESET ROLE`); err != nil {
		return err
	}
	return nil
}

func setOrg(ctx context.Context, conn *pgxpool.Conn, orgID uuid.UUID) error {
	_, err := conn.Exec(ctx, fmt.Sprintf("SET app.current_org = '%s'", orgID.String()))
	return err
}

func acquireAsAppUser(t *testing.T, pool *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "SET ROLE app_user")
	require.NoError(t, err)
	t.Cleanup(func() {
		conn.Exec(ctx, "RESET ROLE")
		conn.Release()
	})
	return conn
}

func TestRLS_IsolatesOrganizations(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, setOrg(ctx, conn, org1ID))

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

func TestRLS_SessionDefaultPopulatesOrgID(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, setOrg(ctx, conn, org1ID))

	programs, err := queries.ListPrograms(ctx)
	require.NoError(t, err)
	require.Len(t, programs, 1)

	// Create a transfer WITHOUT explicitly passing organization_id.
	// The column default current_setting('app.current_org') populates it.
	transfer, err := queries.CreateTransfer(ctx, &db.CreateTransferParams{
		ProgramID:   programs[0].ID,
		Amount:      5000,
		Description: strPtr("Auto-populated org"),
	})
	require.NoError(t, err)
	assert.Equal(t, org1ID, transfer.OrganizationID,
		"organization_id should be auto-populated from session variable")

	entry, err := queries.CreateLedgerEntry(ctx, &db.CreateLedgerEntryParams{
		TransferID: transfer.ID,
		Amount:     5000,
		EntryType:  "debit",
	})
	require.NoError(t, err)
	assert.Equal(t, org1ID, entry.OrganizationID,
		"organization_id should be auto-populated from session variable")
}

func TestRLS_SwitchingOrgContext(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, setOrg(ctx, conn, org1ID))
	transfers1, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, transfers1, 1)

	require.NoError(t, setOrg(ctx, conn, org2ID))
	transfers2, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, transfers2, 1)
	assert.Equal(t, "Transfer Two", *transfers2[0].Description)
}

func TestRLS_InsertBlockedForWrongOrg(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, setOrg(ctx, conn, org1ID))

	_, err = queries.CreateProgram(ctx, &db.CreateProgramParams{
		OrganizationID: org2ID,
		Name:           "Sneaky Program",
	})
	assert.Error(t, err, "inserting a row for a different org should fail")
}

func TestRLS_GetByIDReturnsNothingForWrongOrg(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, setOrg(ctx, conn, org2ID))
	transfers, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	org2TransferID := transfers[0].ID

	require.NoError(t, setOrg(ctx, conn, org1ID))
	_, err = queries.GetTransfer(ctx, org2TransferID)
	assert.ErrorIs(t, err, pgx.ErrNoRows,
		"fetching another org's row by ID should return no rows")
}

func TestRLS_NoSessionVariableReturnsNothing(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

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

func TestRLS_BypassRoleSeesEverything(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	conn, err := tdb.GetTestPool(t).Pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Release() })

	_, err = conn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

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
