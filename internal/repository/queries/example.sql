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

-- name: CreateExampleWithTaskKey :one
-- task_key 冲突（同一任务重复执行）时不插入、不返回行，调用方据此识别重复。
INSERT INTO examples (name, task_key)
VALUES (sqlc.arg(name), sqlc.arg(task_key)::varchar)
ON CONFLICT (task_key) DO NOTHING
RETURNING id, name, created_at, updated_at;
