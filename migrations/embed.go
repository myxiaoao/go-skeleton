// Package migrations 持有版本化的 SQL 迁移文件，并通过 //go:embed 把它们打进
// 二进制——和 internal/oapi 的 spec 一样，部署时无需额外拷贝 SQL 目录，Docker /
// systemd 路径零改动。cmd/migrate 用 goose 库 API 消费 FS 执行 up/down/status。
//
// 新增迁移：跑 `make migrate-create name=add_email_to_examples` 生成一个时间戳
// 前缀的空文件（goose 时间戳风格、对齐 Laravel，形如
// 20260521143022_add_email_to_examples.sql），再填 SQL。版本号是文件名首个 `_`
// 前的纯数字段（连写的 YYYYMMDDHHMMSS），不要在时间戳里插下划线。文件内用 goose
// 注解分隔 Up/Down：
//
//	-- +goose Up
//	ALTER TABLE examples ADD COLUMN email VARCHAR(255);
//	-- +goose Down
//	ALTER TABLE examples DROP COLUMN email;
//
// 真相源是这些 SQL 文件，不是 Go struct——改表结构走"写迁移文件 + 跑 migrate"，
// 不要靠改 model 等 AutoMigrate（已移除）。
package migrations

import "embed"

// FS 嵌入本目录下所有 .sql 迁移文件。goose 用 SetBaseFS(FS) + 目录参数 "."
// 读取它们。
//
//go:embed *.sql
var FS embed.FS
