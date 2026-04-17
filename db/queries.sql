-- name: GetOrganization :one
SELECT
    *
FROM
    organization
WHERE
    id = sqlc.arg('id');

-- name: ListOrganizations :many
SELECT
    *
FROM
    organization
ORDER BY
    created_at;

-- name: CreateOrganization :one
INSERT INTO organization(
    id,
    name)
VALUES (
    sqlc.arg('id'),
    sqlc.arg('name'))
RETURNING
    *;

-- name: GetProgram :one
SELECT
    *
FROM
    program
WHERE
    id = sqlc.arg('id');

-- name: ListPrograms :many
SELECT
    *
FROM
    program
ORDER BY
    created_at;

-- name: CreateProgram :one
INSERT INTO program(
    organization_id,
    name)
VALUES (
    sqlc.arg('organization_id'),
    sqlc.arg('name'))
RETURNING
    *;

-- name: GetTransfer :one
SELECT
    *
FROM
    transfer
WHERE
    id = sqlc.arg('id');

-- name: ListTransfers :many
SELECT
    *
FROM
    transfer
ORDER BY
    created_at;

-- name: CreateTransfer :one
-- organization_id is auto-populated from the session variable via column default
INSERT INTO transfer(
    program_id,
    amount,
    description)
VALUES (
    sqlc.arg('program_id'),
    sqlc.arg('amount'),
    sqlc.arg('description'))
RETURNING
    *;

-- name: GetLedgerEntry :one
SELECT
    *
FROM
    ledger_entry
WHERE
    id = sqlc.arg('id');

-- name: ListLedgerEntries :many
SELECT
    *
FROM
    ledger_entry
ORDER BY
    created_at;

-- name: CreateLedgerEntry :one
-- organization_id is auto-populated from the session variable via column default
INSERT INTO ledger_entry(
    transfer_id,
    amount,
    entry_type)
VALUES (
    sqlc.arg('transfer_id'),
    sqlc.arg('amount'),
    sqlc.arg('entry_type'))
RETURNING
    *;
