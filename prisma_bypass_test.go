package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/harrisoncramer/rls-example/db"
	"github.com/harrisoncramer/rls-example/internal/dbtest"
)

func TestPrismaBypass_AdminCanReadAllTenants(t *testing.T) {
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

	transfers, err := queries.ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, transfers, 2)

	entries, err := queries.ListLedgerEntries(ctx)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

func TestPrismaBypass_AdminCanWriteToAnyTenant(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	conn, err := tdb.GetTestPool(t).Pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Release() })

	_, err = conn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	queries := db.New(conn)

	_, err = queries.CreateProgram(ctx, &db.CreateProgramParams{
		OrganizationID: org1ID,
		Name:           "Admin Program for Org1",
	})
	require.NoError(t, err)

	_, err = queries.CreateProgram(ctx, &db.CreateProgramParams{
		OrganizationID: org2ID,
		Name:           "Admin Program for Org2",
	})
	require.NoError(t, err)

	programs, err := queries.ListPrograms(ctx)
	require.NoError(t, err)
	assert.Len(t, programs, 4)
}

func TestPrismaBypass_SamePoolDifferentRoles(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	pool := tdb.GetTestPool(t).Pool

	adminConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = adminConn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	adminTransfers, err := db.New(adminConn).ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, adminTransfers, 2)
	adminConn.Release()

	appConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = appConn.Exec(ctx, "SET ROLE app_user")
	require.NoError(t, err)
	_, err = appConn.Exec(ctx, fmt.Sprintf("SET app.current_org = '%s'", org1ID.String()))
	require.NoError(t, err)

	appTransfers, err := db.New(appConn).ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, appTransfers, 1)
	appConn.Release()

	adminConn2, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = adminConn2.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	adminTransfers2, err := db.New(adminConn2).ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, adminTransfers2, 2, "admin connection should not inherit app_user restrictions")
	adminConn2.Release()
}

func TestPrismaBypass_AdminRunsCrossTenantOperations(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	conn, err := tdb.GetTestPool(t).Pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Release() })

	_, err = conn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	newOrgID := uuid.New()
	_, err = conn.Exec(ctx, `INSERT INTO organization (id, name) VALUES ($1, 'Org Three')`, newOrgID)
	require.NoError(t, err)

	_, err = conn.Exec(ctx, `INSERT INTO program (organization_id, name) VALUES ($1, 'New Program')`, newOrgID)
	require.NoError(t, err)

	queries := db.New(conn)
	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 3)
}
