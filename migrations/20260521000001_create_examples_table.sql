-- +goose Up
-- 首版迁移：examples 表，对齐 internal/model.Example 的字段与类型。
-- 用 IF NOT EXISTS 让已存在该表的旧库（此前跑过 AutoMigrate）也能安全 up——
-- 表已在则跳过建表、只登记版本，不报错。
CREATE TABLE IF NOT EXISTS examples (
    id         BIGSERIAL    PRIMARY KEY,
    name       VARCHAR(255) NOT NULL,
    created_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS examples;
