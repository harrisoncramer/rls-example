# RLS Proof of Concept

Proof of concept for PostgreSQL Row-Level Security with shared tables. Demonstrates a phased migration approach for adding RLS to tables that are indirectly scoped through foreign key chains, using transaction-scoped `SET LOCAL` and column defaults to auto-populate `organization_id` without changing insert codepaths.

## Setup

```bash
mise install
mise run setup
```

Or step by step:

```bash
mise run db:up        # Start PostgreSQL 16 in Docker on port 5433
mise run db:migrate   # Apply all 6 migration phases
mise run sqlc:generate # Generate type-safe Go code from SQL queries
```

## Run the tests

```bash
mise run test
```

## Run the server

```bash
mise run server
```

Then:
```bash
# Create an org (admin endpoint, no org header needed)
curl -X POST localhost:8080/admin/organizations \
  -H "Content-Type: application/json" \
  -d '{"name": "Acme Corp"}'

# Create a program (scoped, org_id auto-populated from header)
curl -X POST localhost:8080/programs \
  -H "Content-Type: application/json" \
  -H "X-Organization-ID: <org-id>" \
  -d '{"name": "Grants Program"}'

# List transfers (scoped, only sees this org's data)
curl localhost:8080/transfers -H "X-Organization-ID: <org-id>"

# Admin sees all orgs
curl localhost:8080/admin/organizations
```

## How it works

### Schema

Four tables model the indirect scoping problem:

- `organization` - root tenant
- `program` - originally has direct `organization_id`, now auto-populated from session variable
- `transfer` - originally only has `program_id` (1 hop to org)
- `ledger_entry` - originally only has `transfer_id` (2 hops to org)

After the migration, all tenant-scoped tables have a denormalized `organization_id` with a column default of `current_setting('app.current_org', true)::uuid`. Inserts never pass `organization_id` explicitly.

### Transaction-scoped tenant context (SET LOCAL)

Every request is wrapped in a transaction. The middleware uses `SET LOCAL` to set the role and org context:

```sql
BEGIN;
SET LOCAL ROLE app_user;
SET LOCAL app.current_org = '<organization_id>';
-- all queries here are scoped to this tenant
COMMIT; -- context is automatically discarded
```

`SET LOCAL` is automatically discarded when the transaction commits or rolls back. There's no risk of leaking tenant context between pool checkouts. This is the industry standard pattern used by Supabase, PostgREST, and Citus.

### Defense in depth

Three layers of protection:

1. **Pool level** (`rls.ConfigurePool`): every new connection defaults to `app_user` via `AfterConnect`. A codepath that bypasses the transaction middleware still runs as `app_user` with no org — seeing nothing.
2. **Transaction level**: `SET LOCAL` scopes the role and org to the transaction lifetime. No explicit cleanup needed.
3. **Database level**: RLS policies enforce `organization_id = current_setting('app.current_org')::uuid`. NULL matches nothing (default deny).

### Middleware stack

- **`Conn()`** - Global. Starts a transaction per request, sets `app.current_org` from `X-Organization-ID` header when present.
- **`RequireOrg()`** - Per-group. Rejects requests without the org header. No DB work.
- **`Admin()`** - Per-group. Upgrades the transaction's role to `app_system` (BYPASSRLS).

`Conn()` and `Admin()` operate on the same transaction. A route registered outside both groups gets a transaction but with no org set — it sees nothing.

### Roles

- `app_user` (NOBYPASSRLS): subject to RLS policies. Default for all connections.
- `app_system` (BYPASSRLS): for admin operations, cross-tenant background jobs, data backfills.

In production, these would be dedicated Postgres LOGIN users with separate connection pools. In the test suite, we use a single pool (as `postgres`) and `SET LOCAL ROLE` to simulate both.

### Migrations

Six phases, each a separate migration:

1. `00001_init` - Base tables. `transfer` and `ledger_entry` lack `organization_id`.
2. `00002_add_org_columns` - Add nullable `organization_id` (metadata-only, no table rewrite).
3. `00003_connection_layer` - Create roles, set column defaults to `current_setting('app.current_org', true)::uuid`.
4. `00004_backfill` - Backfill existing rows from parent FK chains.
5. `00005_constraints` - Add NOT NULL constraints and indexes.
6. `00006_rls` - Enable RLS policies on all tenant-scoped tables.

### Queries

Queries are split into two files:

- `db/app_queries.sql` - Normal app path. Inserts omit `organization_id` (populated from session variable).
- `db/admin_queries.sql` - Admin path. Inserts explicitly accept `organization_id` for seeding and backfills.

## Test coverage

### RLS unit tests (rls_test.go)

- Tenant isolation across all four tables
- Session variable default auto-populates `organization_id` on insert
- Context switching between orgs
- Cross-tenant insert blocked by RLS INSERT policy
- Fetch by ID returns nothing for wrong org
- No session variable = no rows (default deny)
- Bypass role sees everything

### Admin bypass tests (prisma_bypass_test.go)

- Admin reads/writes across all tenants
- Same pool serves both admin and app connections with no state leakage
- Admin runs cross-tenant bulk operations

### Adversarial tests (adversarial_test.go)

- Transaction isolation: SET LOCAL doesn't leak between transactions
- 50 concurrent goroutines with different orgs on the same pool
- Fabricated org ID returns empty (not error)
- UPDATE/DELETE blocked for wrong org's rows
- Direct SQL (bypassing SQLC) still filtered by RLS
- Savepoint rollback restores previous org context
- Nil UUID matches nothing
- 100 rapid context switches with no stale state

### Server tests (server/server_test.go)

- Full HTTP stack isolation between orgs
- Transfer and ledger entry `organization_id` auto-populated through middleware
- Admin endpoint sees all tenants
- Missing/invalid org header returns 400
- Hardened pool default deny (naked handler sees nothing)

### Server adversarial tests (server/adversarial_test.go)

- 20 concurrent HTTP requests with different orgs
- 50 rapid sequential org switches
- Fabricated org ID returns empty via HTTP
- Admin context doesn't leak to subsequent scoped request
- Scoped context doesn't leak to subsequent admin request
- Data created by org1 is immediately invisible to org2

### Worker tests (workers/workers_test.go)

- Scoped worker creates transfer tagged with correct org from job args
- Scoped worker cannot see other org's data
- Admin worker sees all transfers across all tenants
- Scoped worker transaction rolls back on error (no partial writes)
- 20 concurrent scoped workers (10 per org) don't interfere

## River integration

Background jobs use the same RLS primitives as the HTTP layer. The `rls` package provides two helpers for workers:

- `rls.WithScopedTx(ctx, pool, orgID, fn)` — starts a scoped transaction from the org in job args, calls `fn` with the transaction, commits on success, rolls back on error.
- `rls.WithAdminTx(ctx, pool, fn)` — starts an admin transaction for cross-tenant work.

Scoped workers pull `organization_id` from their job args and pass it to `WithScopedTx`. The transaction handles `SET LOCAL ROLE app_user` and `SET LOCAL app.current_org`. Admin workers call `WithAdminTx` which sets `SET LOCAL ROLE app_system`.

See `workers/workers.go` for example implementations of both patterns.

## Research

The transaction-scoped `SET LOCAL` approach used in this project is the industry standard for RLS with connection pooling. Here are the primary sources.

### PostgreSQL official docs

The [SET documentation](https://www.postgresql.org/docs/current/sql-set.html) defines `SET LOCAL` behavior:

> "The effects of SET LOCAL last only till the end of the current transaction, whether committed or not."

> "After COMMIT or ROLLBACK, the session-level setting takes effect again."

This is the foundation of the approach. `SET LOCAL` is guaranteed to be discarded when the transaction ends, eliminating any risk of tenant context leaking between pool checkouts.

### PostgREST

PostgREST is the reference implementation for this pattern. Their [transactions documentation](https://postgrest.org/en/stable/references/transactions.html) states:

> "After User Impersonation, every request to an API resource runs inside a transaction."

JWT claims are accessible as transaction-scoped settings:

> `current_setting('request.jwt.claims', true)::json->>'email'`

PostgREST applies role settings as "transaction-scoped settings" per their [authorization docs](https://postgrest.org/en/stable/explanations/db_authz.html). The role is set per-request and accessible via `current_role` or `current_setting('role', true)`.

### Supabase

Supabase uses PostgREST under the hood. Their [edge function guide](https://marmelab.com/blog/2025/12/08/supabase-edge-function-transaction-rls.html) documents how to enforce RLS with direct database connections:

> "To enforce RLS, we need to set the auth.uid parameter for the current session just after starting the transaction"

The implementation uses `SET LOCAL ROLE authenticated` and `SET LOCAL request.jwt.claim.sub` inside transactions, exactly the pattern we follow.

### PgBouncer

The [PgBouncer features page](https://www.pgbouncer.org/features.html) explicitly lists `SET/RESET` as "Never" compatible with transaction pooling mode:

> "A server connection is assigned to a client only during a transaction. When PgBouncer notices that the transaction is over, the server will be put back into the pool."

> "This mode breaks a few session-based features of PostgreSQL."

Session-level `SET` is incompatible with transaction pooling. `SET LOCAL` is the only safe alternative.

### pgx (Go)

The [pgx GitHub discussion on multi-tenant databases](https://github.com/jackc/pgx/issues/288) documents the challenge:

> Using PostgreSQL's RLS requires "a way to set the user on every database connection" to ensure "data are seamlessly dealt out to the correct tenant."

The `AfterConnect` hook "cannot accept dynamic parameters per-request," making transaction-scoped `SET LOCAL` the recommended approach over session-level `SET`.

### Go RLS guide

The [PostgreSQL RLS in Go](https://dev.to/__8fa66572/postgresql-rls-in-go-architecting-secure-multi-tenancy-4ifm) article on DEV.to explains the core problem:

> "We cannot simply use SET app.current_tenant on a connection because connections are pooled. If a connection returns to the pool with the variable set, the next user might inherit those privileges."

Their solution uses `set_config` with `is_local=true` inside transactions:

> "The third parameter 'true' (is_local) means this setting lives ONLY until the end of the transaction."

### AWS

The [AWS Database Blog](https://aws.amazon.com/blogs/database/multi-tenant-data-isolation-with-postgresql-row-level-security/) covers multi-tenant data isolation with PostgreSQL RLS, recommending session-variable-based policies with `current_setting()` for tenant filtering.

### Nile

[Nile's multi-tenant RLS guide](https://www.thenile.dev/blog/multi-tenant-rls) documents their experience shipping RLS in production. They note a gotcha with thread-local storage leaking between requests, which reinforces why transaction-scoped `SET LOCAL` is safer than session-level approaches.

## Teardown

```bash
mise run db:down   # Stop PostgreSQL
mise run db:reset  # Drop volumes and restart
```
