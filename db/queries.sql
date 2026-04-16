-- name: GetOrganization :one
SELECT * FROM organization WHERE id = sqlc.arg('id');

-- name: ListOrganizations :many
SELECT * FROM organization ORDER BY created_at;

-- name: CreateOrganization :one
INSERT INTO organization (id, name) VALUES (sqlc.arg('id'), sqlc.arg('name')) RETURNING *;

-- name: GetAccount :one
SELECT * FROM account WHERE id = sqlc.arg('id');

-- name: ListAccounts :many
SELECT * FROM account ORDER BY created_at;

-- name: CreateAccount :one
INSERT INTO account (organization_id, email) VALUES (sqlc.arg('organization_id'), sqlc.arg('email')) RETURNING *;

-- name: GetProject :one
SELECT * FROM project WHERE id = sqlc.arg('id');

-- name: ListProjects :many
SELECT * FROM project ORDER BY created_at;

-- name: CreateProject :one
INSERT INTO project (organization_id, name, description) VALUES (sqlc.arg('organization_id'), sqlc.arg('name'), sqlc.arg('description')) RETURNING *;
