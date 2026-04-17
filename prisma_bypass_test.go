package main

// These tests demonstrate how an admin/system connection bypasses RLS.
//
// In a real codebase, there are two categories of database access:
//
//  1. Service connections (app_user role): Normal request handling in Go services.
//     These are subject to RLS and must set app.current_org on every connection
//     checkout. This is the common path — API requests, webhook handlers, etc.
//
//  2. Admin connections (app_system role): Prisma migrations, admin
//     queries, cross-tenant background jobs (batch settlement, reconciliation,
//     River workers that span orgs), data backfills, and analytics. These bypass
//     RLS entirely and see all data without needing a session variable.
//
// The tests below verify that the app_system role works correctly alongside
// app_user on the same connection pool, with no state leakage between them.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/harrisoncramer/rls-example/db"
	"github.com/harrisoncramer/rls-example/internal/dbtest"
	"github.com/harrisoncramer/rls-example/internal/rls"
)

// TestPrismaBypass_AdminCanReadAllTenants verifies that the app_system role
// sees all data across every tenant without setting app.current_org. This is
// what an admin connection does when running cross-tenant queries — it needs
// to read and write across org boundaries.
func TestPrismaBypass_AdminCanReadAllTenants(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAdmin(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
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

// TestPrismaBypass_AdminCanWriteToAnyTenant verifies that the admin role can
// insert rows into any organization. This is needed for operations like creating
// new programs during onboarding, seeding sandbox data, or running data
// migrations that touch multiple tenants in a single transaction.
func TestPrismaBypass_AdminCanWriteToAnyTenant(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAdmin(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	_, err = queries.AdminCreateProgram(ctx, &db.AdminCreateProgramParams{
		OrganizationID: org1ID,
		Name:           "Admin Program for Org1",
	})
	require.NoError(t, err)

	_, err = queries.AdminCreateProgram(ctx, &db.AdminCreateProgramParams{
		OrganizationID: org2ID,
		Name:           "Admin Program for Org2",
	})
	require.NoError(t, err)

	programs, err := queries.ListPrograms(ctx)
	require.NoError(t, err)
	assert.Len(t, programs, 4)
}

// TestPrismaBypass_SamePoolDifferentRoles verifies that role state doesn't
// leak between pool checkouts. In production, we'd run separate pools with
// dedicated Postgres login roles (app_user for scoped, app_system for admin).
// In tests, we share a single pool and use SET ROLE to simulate both.
//
// The test verifies three things:
//  1. An admin connection sees all data (2 transfers across both orgs)
//  2. An app_user connection with org1 context sees only org1's data (1 transfer)
//  3. A new admin connection acquired AFTER the app_user connection was released
//     does NOT inherit the app_user's restrictions or session variable
//
// Point 3 is the critical one: if session state leaked between pool checkouts,
// one tenant could see another's data.
func TestPrismaBypass_SamePoolDifferentRoles(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	pool := tdb.GetTestPool(t).Pool

	// Connection 1: admin, sees everything.
	// We use pool.Acquire + rls.SetRole directly (instead of AcquireAsAdmin)
	// because this test needs to manually release and re-acquire connections
	// mid-test to prove there's no state leakage between pool checkouts.
	// AcquireAsAdmin registers a t.Cleanup that releases on test end, but
	// pgxpool panics on double release.
	adminConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	require.NoError(t, rls.SetRole(ctx, adminConn, "app_system"))

	adminTransfers, err := db.New(adminConn).ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, adminTransfers, 2)
	adminConn.Release()

	// Connection 2: app_user with org1 context, sees only org1
	appConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	require.NoError(t, rls.SetRole(ctx, appConn, "app_user"))
	require.NoError(t, rls.SetOrg(ctx, appConn, org1ID))

	appTransfers, err := db.New(appConn).ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, appTransfers, 1)
	appConn.Release()

	// Connection 3: new admin — must NOT carry over app_user state from connection 2.
	// If the pool reuses the same underlying connection, SET ROLE app_system must
	// override whatever was set before.
	adminConn2, err := pool.Acquire(ctx)
	require.NoError(t, err)
	require.NoError(t, rls.SetRole(ctx, adminConn2, "app_system"))

	adminTransfers2, err := db.New(adminConn2).ListTransfers(ctx)
	require.NoError(t, err)
	assert.Len(t, adminTransfers2, 2, "admin connection should not inherit app_user restrictions")
	adminConn2.Release()
}

// TestPrismaBypass_AdminRunsCrossTenantOperations simulates the kind of work
// that a system job would do: creating a brand new organization and seeding it
// with data, then reading across all tenants to verify. This is a common
// pattern for:
//   - Onboarding a new nonprofit (create org, program, financial accounts)
//   - Running batch settlement across all orgs
//   - Data migrations that backfill a new column across every tenant
//   - Analytics/reporting queries that aggregate across the platform
func TestPrismaBypass_AdminRunsCrossTenantOperations(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn, err := rls.AcquireAsAdmin(t, tdb.GetTestPool(t).Pool)
	require.NoError(t, err)
	ctx := context.Background()
	queries := db.New(conn)

	newOrgID := uuid.New()
	_, err = queries.CreateOrganization(ctx, &db.CreateOrganizationParams{
		ID: newOrgID, Name: "Org Three",
	})
	require.NoError(t, err)

	_, err = queries.AdminCreateProgram(ctx, &db.AdminCreateProgramParams{
		OrganizationID: newOrgID, Name: "New Program",
	})
	require.NoError(t, err)

	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 3)
}
