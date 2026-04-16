# RLS Proof o Concept

Proof of concept for PostgreSQL Row-Level Security with shared tables. Demonstrates how RLS policies enforce tenant isolation at the database level using session variables, and how admin connections (like Prisma) bypass RLS for migrations and cross-tenant operations.

## Setup

Install tools (Go, Node, Prisma, SQLC) and start the database, run migrations, and generate code in one shot:

```bash
mise install
mise run setup
```

Or step by step:

## Start the database

Spins up PostgreSQL 16 in Docker on port 5433.

```bash
mise run db:up
```

## Run migrations

Prisma manages the schema and migrations. There are two:

1. `00001_init` creates the tables (organization, account, project) with `organization_id` on every tenant-scoped table
2. `00002_rls` enables RLS, creates per-table policies, and sets up two roles: `app_user` (subject to RLS) and `app_system` (bypasses RLS)

```bash
mise run db:migrate
```

## Generate SQLC code

SQLC generates type-safe Go code from the SQL queries in `db/queries.sql`. The generated code lives in `db/`.

```bash
mise run sqlc:generate
```

## Run the tests

The test suite uses Blueprint for copy-on-write test databases. Each test gets its own isolated database with migrations already applied. Env vars are configured in mise.toml.

```bash
mise run test
```

## How it works

### Roles

There are three roles in play:

- `postgres` (superuser): Owns the tables. Used by Prisma for migrations. Superusers always bypass RLS regardless of FORCE ROW LEVEL SECURITY.
- `app_user` (NOLOGIN): The role that Go services use via `SET ROLE`. Subject to RLS policies. Can only see rows belonging to the org set in the session variable.
- `app_system` (NOLOGIN, BYPASSRLS): The role for admin/system operations. Used for background jobs that span tenants, data backfills, etc. Bypasses all RLS policies.

### Session variable

Before executing queries, a service sets the org context on the connection:

```sql
SET ROLE app_user;
SET app.current_org = '<organization_id>';
```

All subsequent queries on that connection are automatically filtered by the RLS policies. No need to pass `organization_id` as a query parameter or join through parent tables.

### Policies

Every tenant-scoped table gets policies for SELECT, INSERT, UPDATE, and DELETE. They all check the same thing:

```sql
CREATE POLICY tenant_isolation_select ON project
    FOR SELECT USING (organization_id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_insert ON project
    FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org', true)::uuid);
```

The `true` argument to `current_setting` makes it return NULL instead of erroring when the variable isn't set. NULL doesn't match any UUID, so if you forget to set the session variable, you see nothing (default deny).

### Admin bypass (Prisma)

Prisma and other admin connections use the `app_system` role which has `BYPASSRLS`. This lets them:

- Run migrations
- Do cross-tenant queries (analytics, reporting)
- Seed data
- Run background jobs that touch multiple orgs

In the test suite, `prisma_bypass_test.go` demonstrates this. The admin role sees all rows, can write to any org, and can fetch any row by ID without setting a session variable.

### Connection pooling

Session variables are per-connection, not per-query. With pgxpool, you need to acquire a specific connection, set the role and org, run your queries, then release it. The test helper `acquireAsAppUser` shows this pattern.

In production, you'd set up BeforeAcquire/AfterRelease hooks on the pool to automatically set and clear the session variables, or use `SET LOCAL` inside transactions so the variable is scoped to the transaction and automatically cleared on commit/rollback.

## Test coverage

### RLS tests (rls_test.go)

- `TestRLS_IsolatesOrganizations`: With org1 context, only org1's orgs/accounts/projects are visible
- `TestRLS_SwitchingOrgContext`: Switching session variable changes which rows are returned
- `TestRLS_InsertBlockedForWrongOrg`: Can't insert a row with a different org's organization_id
- `TestRLS_GetByIDReturnsNothingForWrongOrg`: Fetching by primary key returns no rows if it belongs to another org
- `TestRLS_NoSessionVariableReturnsNothing`: Without setting the session variable, all queries return empty (default deny)
- `TestRLS_BypassRoleSeesEverything`: The app_system role sees all data across all tenants
- `TestRLS_InsertAndReadBack`: Normal CRUD works when session matches the row's org

### Admin bypass tests (prisma_bypass_test.go)

- `TestPrismaBypass_AdminCanReadAllTenants`: Admin role reads all orgs without session variable
- `TestPrismaBypass_AdminCanWriteToAnyTenant`: Admin role inserts into any org
- `TestPrismaBypass_AdminCanFetchAnyRowByID`: Admin role fetches specific rows by ID across tenants
- `TestPrismaBypass_SamePoolDifferentRoles`: Same pool serves both admin and app connections with different roles, no state leakage
- `TestPrismaBypass_AdminRunsMigrationStyleQueries`: Admin role does cross-tenant bulk operations (simulating migrations/backfills)

## Teardown

```bash
mise run db:down
```

Reset everything (drops volumes):

```bash
mise run db:reset
```
