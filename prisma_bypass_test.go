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

// These tests demonstrate how an admin connection (like Prisma's) would bypass
// RLS by using the app_system role. In our real codebase, Prisma connects as
// an admin user for migrations and orchestration queries. That connection would
// use SET ROLE app_system to bypass RLS entirely.

func TestPrismaBypass_AdminCanReadAllTenants(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	conn, err := tdb.GetTestPool(t).Pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Release() })

	// Prisma connects as the admin/superuser and uses the bypass role
	_, err = conn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	queries := db.New(conn)

	// Admin sees all orgs without setting app.current_org
	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 2)

	// Admin sees all accounts across tenants
	accounts, err := queries.ListAccounts(ctx)
	require.NoError(t, err)
	assert.Len(t, accounts, 3)

	// Admin sees all projects across tenants
	projects, err := queries.ListProjects(ctx)
	require.NoError(t, err)
	assert.Len(t, projects, 3)
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

	// Admin can insert into any org without setting session variable
	_, err = queries.CreateProject(ctx, &db.CreateProjectParams{
		OrganizationID: org1ID,
		Name:           "Admin Created for Org1",
		Description:    strPtr("Created by admin"),
	})
	require.NoError(t, err)

	_, err = queries.CreateProject(ctx, &db.CreateProjectParams{
		OrganizationID: org2ID,
		Name:           "Admin Created for Org2",
		Description:    strPtr("Created by admin"),
	})
	require.NoError(t, err)

	projects, err := queries.ListProjects(ctx)
	require.NoError(t, err)
	assert.Len(t, projects, 5) // 3 seeded + 2 admin-created
}

func TestPrismaBypass_AdminCanFetchAnyRowByID(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()

	// First, as app_user with org1 context, get org1's project IDs
	appConn, err := tdb.GetTestPool(t).Pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = appConn.Exec(ctx, "SET ROLE app_user")
	require.NoError(t, err)
	_, err = appConn.Exec(ctx, fmt.Sprintf("SET app.current_org = '%s'", org1ID.String()))
	require.NoError(t, err)

	appQueries := db.New(appConn)
	org1Projects, err := appQueries.ListProjects(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, org1Projects)
	org1ProjectID := org1Projects[0].ID
	appConn.Release()

	// Now as admin, fetch that specific project without any session variable
	adminConn, err := tdb.GetTestPool(t).Pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { adminConn.Release() })
	_, err = adminConn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	adminQueries := db.New(adminConn)
	project, err := adminQueries.GetProject(ctx, org1ProjectID)
	require.NoError(t, err)
	assert.Equal(t, org1ProjectID, project.ID)
}

func TestPrismaBypass_SamePoolDifferentRoles(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	pool := tdb.GetTestPool(t).Pool

	// This simulates a real setup where the same pool serves both
	// admin (Prisma/orchestration) and app (Go service) connections.
	// Each connection sets its own role.

	// Connection 1: admin role, sees everything
	adminConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = adminConn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	adminOrgs, err := db.New(adminConn).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, adminOrgs, 2)
	adminConn.Release()

	// Connection 2: app_user role with org1 context, sees only org1
	appConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = appConn.Exec(ctx, "SET ROLE app_user")
	require.NoError(t, err)
	_, err = appConn.Exec(ctx, fmt.Sprintf("SET app.current_org = '%s'", org1ID.String()))
	require.NoError(t, err)

	appOrgs, err := db.New(appConn).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, appOrgs, 1)
	appConn.Release()

	// Connection 3: new admin connection, should not carry over
	// the app_user state from connection 2 (pool resets per checkout)
	adminConn2, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = adminConn2.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	adminOrgs2, err := db.New(adminConn2).ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, adminOrgs2, 2, "admin connection should not inherit app_user restrictions")
	adminConn2.Release()
}

func TestPrismaBypass_AdminRunsMigrationStyleQueries(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	conn, err := tdb.GetTestPool(t).Pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Release() })

	_, err = conn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	// Admin can run cross-tenant operations like bulk updates,
	// which is what Prisma would do during migrations or data backfills
	newOrgID := uuid.New()
	_, err = conn.Exec(ctx, `INSERT INTO organization (id, name) VALUES ($1, 'Org Three')`, newOrgID)
	require.NoError(t, err)

	// Bulk insert accounts across orgs
	_, err = conn.Exec(ctx, `INSERT INTO account (organization_id, email) VALUES ($1, 'new@org3.com')`, newOrgID)
	require.NoError(t, err)

	// Count everything
	queries := db.New(conn)
	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 3)

	accounts, err := queries.ListAccounts(ctx)
	require.NoError(t, err)
	assert.Len(t, accounts, 4) // 3 seeded + 1 new
}
