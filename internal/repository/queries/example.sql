-- name: CreateExample :one
INSERT INTO examples (name)
VALUES (sqlc.arg(name))
RETURNING id, name, created_at, updated_at;

-- name: CountExamples :one
SELECT count(*) FROM examples;

-- name: ListExamples :many
SELECT id, name, created_at, updated_at
FROM examples
ORDER BY id DESC
LIMIT sqlc.arg(lim)::bigint OFFSET sqlc.arg(off)::bigint;
