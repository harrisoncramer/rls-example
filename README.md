# RLS Proof of Concept

Proof of concept for PostgreSQL Row-Level Security with shared tables. Demonstrates a phased migration approach for adding RLS to tables that are indirectly scoped through foreign key chains, using session variable column defaults to auto-populate `organization_id` without changing insert codepaths.

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

Prisma manages the schema and migrations. There are six, each representing a phase of the migration plan:

1. `00001_init` creates the tables. `organization` and `program` have direct `organization_id`. `transfer` only has `program_id` and `ledger_entry` only has `transfer_id` (indirectly scoped through FK chains).
2. `00002_add_org_columns` adds nullable `organization_id` to `transfer` and `ledger_entry`. Metadata-only operation, no table rewrite.
3. `00003_connection_layer` creates `app_user` and `app_system` roles, then sets column defaults to `current_setting('app.current_org', true)::uuid` so new inserts auto-populate `organization_id` from the session variable.
4. `00004_backfill` backfills existing rows from parent FK chains.
5. `00005_constraints` adds NOT NULL constraints and indexes on the new columns.
6. `00006_rls` enables RLS policies on all tenant-scoped tables.

```bash
mise run db:migrate
```

## Generate SQLC code

SQLC generates type-safe Go code from the SQL queries in `db/queries.sql`. The generated code lives in `db/`.

```bash
mise run sqlc:generate
```

## Run the tests

The test suite uses Blueprint for copy-on-write test databases. Each test gets its own isolated database with all migrations applied. Env vars are configured in mise.toml.

```bash
mise run test
```

## How it works

### Schema

Four tables model the indirect scoping problem:

- `organization` - root tenant
- `program` - has direct `organization_id`
- `transfer` - originally only has `program_id` (1 hop to org)
- `ledger_entry` - originally only has `transfer_id` (2 hops to org)

After the migration, `transfer` and `ledger_entry` both have a denormalized `organization_id` that is auto-populated from the session variable.

### Session variable column defaults

The key insight: instead of updating every insert codepath to explicitly pass `organization_id`, we set the column default to `current_setting('app.current_org', true)::uuid`. When a service sets `SET app.current_org = '<uuid>'` on connection checkout, all inserts into these tables get `organization_id` for free.

The SQLC-generated `CreateTransfer` and `CreateLedgerEntry` functions don't even have `organization_id` in their params structs. The column default handles it.

### Roles

- `postgres` (superuser): Owns the tables. Used by Prisma for migrations.
- `app_user` (NOLOGIN): The role Go services use via `SET ROLE`. Subject to RLS policies.
- `app_system` (NOLOGIN, BYPASSRLS): For admin/system operations, background jobs that span tenants, data backfills.

### Session variable

Before executing queries, a service sets the org context on the connection:

```sql
SET ROLE app_user;
SET app.current_org = '<organization_id>';
```

All subsequent queries on that connection are automatically filtered by RLS. No need to pass `organization_id` as a query parameter or join through parent tables.

### Policies

Every tenant-scoped table gets policies for SELECT, INSERT, UPDATE, and DELETE:

```sql
CREATE POLICY tenant_isolation_select ON transfer
    FOR SELECT USING (organization_id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_insert ON transfer
    FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org', true)::uuid);
```

The `true` argument to `current_setting` returns NULL instead of erroring when the variable isn't set. NULL doesn't match any UUID, so if you forget to set the session variable, you see nothing (default deny).

### Admin bypass (Prisma)

Prisma and other admin connections use the `app_system` role which has `BYPASSRLS`. This lets them run migrations, do cross-tenant queries, seed data, and run background jobs that touch multiple orgs.

### Connection pooling

Session variables are per-connection, not per-query. With pgxpool, you acquire a connection, set the role and org, run your queries, then release it. In production, you'd set up BeforeAcquire/AfterRelease hooks on the pool to automatically set and clear the session variables.

## Test coverage

### RLS tests (rls_test.go)

- `TestRLS_IsolatesOrganizations`: With org1 context, only org1's data is visible across all four tables
- `TestRLS_SessionDefaultPopulatesOrgID`: Creates transfer and ledger entry without passing `organization_id`, verifies it was auto-populated from the session variable
- `TestRLS_SwitchingOrgContext`: Switching session variable changes which rows are returned
- `TestRLS_InsertBlockedForWrongOrg`: Can't insert a row with a different org's `organization_id`
- `TestRLS_GetByIDReturnsNothingForWrongOrg`: Fetching by primary key returns no rows if it belongs to another org
- `TestRLS_NoSessionVariableReturnsNothing`: Without setting the session variable, all queries return empty (default deny)
- `TestRLS_BypassRoleSeesEverything`: The `app_system` role sees all data across all tenants

### Admin bypass tests (prisma_bypass_test.go)

- `TestPrismaBypass_AdminCanReadAllTenants`: Admin role reads all data without session variable
- `TestPrismaBypass_AdminCanWriteToAnyTenant`: Admin role inserts into any org
- `TestPrismaBypass_SamePoolDifferentRoles`: Same pool serves both admin and app connections, no state leakage
- `TestPrismaBypass_AdminRunsCrossTenantOperations`: Admin role does cross-tenant bulk operations

## Teardown

```bash
mise run db:down
```

Reset everything (drops volumes):

```bash
mise run db:reset
```
