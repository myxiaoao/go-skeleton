-- +goose Up
-- task_key 是 worker 消费 example 任务时的幂等键：asynq 是 at-least-once 投递，
-- INSERT 已提交但 ack 前 worker 崩溃 / lease 过期时，同一任务会再执行一次；
-- 唯一索引 + INSERT ... ON CONFLICT DO NOTHING 保证重复执行只落一行。
-- 列可空：API 直接创建的记录不带 task_key，唯一索引下多个 NULL 互不冲突。
ALTER TABLE examples ADD COLUMN IF NOT EXISTS task_key VARCHAR(64);
CREATE UNIQUE INDEX IF NOT EXISTS examples_task_key_key ON examples (task_key);

-- +goose Down
DROP INDEX IF EXISTS examples_task_key_key;
ALTER TABLE examples DROP COLUMN IF EXISTS task_key;
