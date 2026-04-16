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

// seedTwoOrgs inserts two organizations with accounts and projects.
// Runs as the bypass role so RLS doesn't interfere with seeding.
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

	if _, err := pool.Exec(ctx, `INSERT INTO account (organization_id, email) VALUES ($1, 'alice@org1.com')`, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO account (organization_id, email) VALUES ($1, 'bob@org1.com')`, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO account (organization_id, email) VALUES ($1, 'carol@org2.com')`, org2ID); err != nil {
		return err
	}

	if _, err := pool.Exec(ctx, `INSERT INTO project (organization_id, name, description) VALUES ($1, 'Project Alpha', 'Org 1 project')`, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO project (organization_id, name, description) VALUES ($1, 'Project Beta', 'Another org 1 project')`, org1ID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO project (organization_id, name, description) VALUES ($1, 'Project Gamma', 'Org 2 project')`, org2ID); err != nil {
		return err
	}

	if _, err := pool.Exec(ctx, `RESET ROLE`); err != nil {
		return err
	}
	return nil
}

// setOrg sets the app.current_org session variable on a connection.
// SET doesn't support parameterized queries, so we format the string directly.
// The orgID is a UUID so there's no injection risk.
func setOrg(ctx context.Context, conn *pgxpool.Conn, orgID uuid.UUID) error {
	_, err := conn.Exec(ctx, fmt.Sprintf("SET app.current_org = '%s'", orgID.String()))
	return err
}

// acquireAsAppUser gets a single connection and switches to the app_user role.
// This is important: the postgres superuser bypasses RLS even with FORCE ROW
// LEVEL SECURITY. We need to use a non-superuser role.
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

	accounts, err := queries.ListAccounts(ctx)
	require.NoError(t, err)
	assert.Len(t, accounts, 2)
	for _, a := range accounts {
		assert.Equal(t, org1ID, a.OrganizationID)
	}

	projects, err := queries.ListProjects(ctx)
	require.NoError(t, err)
	assert.Len(t, projects, 2)
	for _, p := range projects {
		assert.Equal(t, org1ID, p.OrganizationID)
	}
}

func TestRLS_SwitchingOrgContext(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, setOrg(ctx, conn, org1ID))
	projects1, err := queries.ListProjects(ctx)
	require.NoError(t, err)
	assert.Len(t, projects1, 2)

	require.NoError(t, setOrg(ctx, conn, org2ID))
	projects2, err := queries.ListProjects(ctx)
	require.NoError(t, err)
	assert.Len(t, projects2, 1)
	assert.Equal(t, "Project Gamma", projects2[0].Name)
}

func TestRLS_InsertBlockedForWrongOrg(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, setOrg(ctx, conn, org1ID))

	_, err = queries.CreateProject(ctx, &db.CreateProjectParams{
		OrganizationID: org2ID,
		Name:           "Sneaky Project",
		Description:    strPtr("Should not be allowed"),
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
	org2Projects, err := queries.ListProjects(ctx)
	require.NoError(t, err)
	require.Len(t, org2Projects, 1)
	org2ProjectID := org2Projects[0].ID

	require.NoError(t, setOrg(ctx, conn, org1ID))
	_, err = queries.GetProject(ctx, org2ProjectID)
	assert.ErrorIs(t, err, pgx.ErrNoRows, "fetching another org's row by ID should return no rows")
}

func TestRLS_NoSessionVariableReturnsNothing(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	// Don't set app.current_org. current_setting returns NULL,
	// which doesn't match any rows.
	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Empty(t, orgs, "no session variable means no rows visible")
}

func TestRLS_BypassRoleSeesEverything(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	ctx := context.Background()
	conn, err := tdb.GetTestPool(t).Pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Release() })

	// Use the bypass role instead of app_user
	_, err = conn.Exec(ctx, "SET ROLE app_system")
	require.NoError(t, err)

	queries := db.New(conn)

	orgs, err := queries.ListOrganizations(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 2, "bypass role should see all organizations")

	projects, err := queries.ListProjects(ctx)
	require.NoError(t, err)
	assert.Len(t, projects, 3, "bypass role should see all projects")

	accounts, err := queries.ListAccounts(ctx)
	require.NoError(t, err)
	assert.Len(t, accounts, 3, "bypass role should see all accounts")
}

func TestRLS_InsertAndReadBack(t *testing.T) {
	tdb, err := dbtest.New(t, seedTwoOrgs)
	require.NoError(t, err)

	conn := acquireAsAppUser(t, tdb.GetTestPool(t).Pool)
	ctx := context.Background()
	queries := db.New(conn)

	require.NoError(t, setOrg(ctx, conn, org1ID))

	created, err := queries.CreateProject(ctx, &db.CreateProjectParams{
		OrganizationID: org1ID,
		Name:           "Project Delta",
		Description:    strPtr("Created during test"),
	})
	require.NoError(t, err)
	assert.Equal(t, "Project Delta", created.Name)

	fetched, err := queries.GetProject(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)

	projects, err := queries.ListProjects(ctx)
	require.NoError(t, err)
	assert.Len(t, projects, 3)

	require.NoError(t, setOrg(ctx, conn, org2ID))
	projects, err = queries.ListProjects(ctx)
	require.NoError(t, err)
	assert.Len(t, projects, 1)
}

func strPtr(s string) *string {
	return &s
}
