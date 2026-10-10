-- +goose Up
-- created_at / updated_at 从 TIMESTAMP 改为 TIMESTAMPTZ。
-- TIMESTAMP 不带时区：DEFAULT CURRENT_TIMESTAMP 存的是会话时区的墙钟时间，
-- pgx 又把 timestamp 一律当 UTC 解码，会话时区非 UTC（如 Asia/Shanghai）时
-- 对外返回的时间会偏移整数个小时。TIMESTAMPTZ 存绝对时刻，不受会话时区影响。
--
-- 存量数据按"当前会话时区下的墙钟时间"解释后转换（与 DEFAULT 写入时的语义
-- 一致）；如果历史数据是在不同会话时区下写入的，需在迁移前自行修正。
-- 向后兼容：sqlc 对 timestamp / timestamptz 都映射成 time.Time，旧代码可直接
-- 读写新列；改列类型会重写整表并持有 ACCESS EXCLUSIVE 锁，大表请在低峰执行。
-- breaking: ALTER COLUMN TYPE 重写整表，数据语义从墙钟时间变为绝对时刻
ALTER TABLE examples ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE current_setting('TimeZone');
ALTER TABLE examples ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE current_setting('TimeZone');

-- +goose Down
ALTER TABLE examples ALTER COLUMN created_at TYPE TIMESTAMP USING created_at AT TIME ZONE current_setting('TimeZone');
ALTER TABLE examples ALTER COLUMN updated_at TYPE TIMESTAMP USING updated_at AT TIME ZONE current_setting('TimeZone');
