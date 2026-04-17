-- Admin queries run under the app_system role (BYPASSRLS). They explicitly
-- accept organization_id rather than relying on the session variable default.
-- Used for seeding, backfills, cross-tenant background jobs, and migrations.

-- name: AdminCreateProgram :one
INSERT INTO program (
    organization_id,
    name)
VALUES (
    sqlc.arg ('organization_id'),
    sqlc.arg ('name'))
RETURNING
    *;

-- name: AdminCreateTransfer :one
INSERT INTO transfer (
    id,
    program_id,
    organization_id,
    amount,
    description)
VALUES (
    sqlc.arg ('id'),
    sqlc.arg ('program_id'),
    sqlc.arg ('organization_id'),
    sqlc.arg ('amount'),
    sqlc.arg ('description'))
RETURNING
    *;

-- name: AdminCreateLedgerEntry :one
INSERT INTO ledger_entry (
    transfer_id,
    organization_id,
    amount,
    entry_type)
VALUES (
    sqlc.arg ('transfer_id'),
    sqlc.arg ('organization_id'),
    sqlc.arg ('amount'),
    sqlc.arg ('entry_type'))
RETURNING
    *;
