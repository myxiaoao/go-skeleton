# GORM → sqlc（pgx/v5）迁移 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把数据访问层从 GORM 换成 sqlc 生成代码 + pgx/v5（pgxpool），SQL 成为唯一真相源，分 3 个 PR 交付。

**Architecture:** `migrations/*.sql` 作为 sqlc schema、`internal/repository/queries/*.sql` 作为查询，生成到 `internal/repository/sqlcdb`（入库）。repository 通过 `DB` 接口（`sqlcdb.DBTX` + `BeginTx`，由 `*pgxpool.Pool` 满足）访问数据库，事务句柄经 ctx 传递；`pkg/database` 持有 pgxpool 并用 pgx `QueryTracer` 输出 SQL 日志。PR1 让 GORM 临时复用同一连接池过渡，PR2 切换并删除 GORM，PR3 更新文档。

**Tech Stack:** Go 1.27.1、pgx/v5 v5.10.0（pgxpool / stdlib / QueryTracer）、sqlc v1.31.1、goose Provider API、zap、标准库 testing。

**Spec:** `docs/superpowers/specs/2026-09-28-sqlc-migration-design.md`

## Global Constraints

- 回复 / 文档用简体中文；**代码注释用简体中文**（技术术语与标识符保持英文；用户 2026-09-28 明确要求，覆盖全局"注释用英文"规则）。下文 Task 代码块里的英文注释在落地时一律译成中文，生成代码（`internal/oapi`、`internal/repository/sqlcdb`）不动。
- 测试只用标准库 `testing` + 手写 mock；**禁止** testify / gomock / mockery / sqlmock / testcontainers。测试 ctx 用 `t.Context()`；错误断言用 `errors.Is` / `errors.AsType`。
- service / handler 禁止 `context.Background()`；repository 所有调用透传 ctx。
- sqlc 版本固定 `v1.31.1`；`sql_package: pgx/v5`；生成目录 `internal/repository/sqlcdb`（DO NOT EDIT，入库）。
- 配置项（无兼容层）：`DB_LOG_LEVEL`（silent/error/warn/info，默认 warn）、`DB_MAX_CONNS`（默认 30，1..MaxInt32）、`DB_MIN_CONNS`（默认 0，0..DB_MAX_CONNS）、`DB_CONN_MAX_LIFETIME`（30m）、`DB_CONN_MAX_IDLE_TIME`（5m）。
- tracer 日志字段：`sql`（模板，不含参数值）、`rows`、`elapsed`；慢查询阈值 200ms；`context.Canceled` 降级为 warn。
- 不引入 query builder；不做 `sqlc vet`；不预定义 `ErrNotFound`。
- 提交：`type(scope): description` + 空行 + 每项一行的详细说明；type ∈ feat/fix/refactor/docs/test/chore。逐文件 `git add`，**禁止** `git add .`；**禁止** `--no-verify`；**不要 push**（push / 开 PR 前征得用户确认）。
- 每个 Task 结束前跑 `make verify`（除非步骤另有说明），全绿才提交。
- 分支：PR1 = 当前 `feat/sqlc-migration`；PR2 / PR3 在前一个 PR 合并后从最新 `master` 拉 `refactor/sqlc-switch`、`docs/sqlc-docs`。

---

## 文件结构总览

| 文件 | PR | 动作 | 职责 |
| --- | --- | --- | --- |
| `sqlc.yaml` | 1 | Create | sqlc 配置 |
| `internal/repository/queries/example.sql` | 1 | Create | example 查询 |
| `internal/repository/sqlcdb/*.go` | 1 | Generate | sqlc 产物 |
| `Makefile` | 1 / 2 | Modify | `_ensure-sqlc` / `sqlc` / `sqlc-verify` / init / verify |
| `.golangci.yml` | 1 | Modify | 排除 sqlcdb |
| `config/{types,config,validate}.go` + 测试 | 1 | Modify | 配置项改名与校验 |
| `.env.example`、`README.md`、`README_en.md` | 1 | Modify | 配置项改名 |
| `pkg/database/tracer.go` + `tracer_test.go` | 1 | Create | pgx QueryTracer |
| `pkg/database/database.go` + `database_test.go` | 1 / 2 | Rewrite | pgxpool manager |
| `pkg/database/gorm_logger*.go` | 1 | Delete | 被 tracer 取代 |
| `internal/bootstrap/{registry,api}.go` | 1 | Modify | InitDatabase / Pool 判空 |
| `cmd/migrate/main.go` | 1 | Modify | 用 `SQLDB()` |
| `internal/server_test.go` | 1 / 2 | Modify | 用 pgxpool 构造 registry |
| `internal/repository/tx.go` + `tx_test.go` | 2 | Rewrite / Create | DB 接口、InTx、TxManager、共享 mock |
| `internal/repository/example.go` + `example_test.go` + `example_integration_test.go` | 2 | Rewrite | sqlc 版 repository |
| `internal/model/example.go`、删 `example_test.go` | 2 | Modify / Delete | 普通 struct、ID int64 |
| `internal/service/tx.go` | 2 | Create | `Transactor` 接口 |
| `internal/service/example.go` | 2 | Modify | `zap.Int64` |
| `internal/server.go`、`internal/worker.go` | 2 | Modify | `reg.DB.Pool()`、Transactor 断言 |
| `internal/worker/handler.go` | 2 | Modify | 注释去 gorm |
| `scripts/architecture-verify.go` + 测试 | 2 | Modify | 规则 2 改 pgx/sqlcdb、新增规则 5 禁 gorm |
| `scripts/new-endpoint.go` | 2 | Modify | repository / model / 测试模板 |
| `scripts/drop-example.go` | 2 | Modify | sqlc 适配 + 修复既有缺陷 |
| `CLAUDE.md`、`AGENTS.md`、`docs/runbook.md`、`docs/development.md`、`CHANGELOG.md` | 3 | Modify | 文档 |

---

# PR1：基础设施（分支 `feat/sqlc-migration`）

### Task 1: sqlc 工具链与 example 查询

**Files:**
- Create: `sqlc.yaml`
- Create: `internal/repository/queries/example.sql`
- Generate: `internal/repository/sqlcdb/{db.go,models.go,example.sql.go}`
- Modify: `Makefile`（版本变量区、`init`、oapi-verify 之后新增 target、`verify`）
- Modify: `.golangci.yml`（两处 `exclusions.paths`）
- Modify: `go.mod` / `go.sum`（`go mod tidy`）

**Interfaces:**
- Produces（后续 Task 依赖的生成 API，名字由 SQL 决定，必须一致）：
  - `sqlcdb.DBTX`（`Exec` / `Query` / `QueryRow`）、`sqlcdb.New(db DBTX) *Queries`
  - `sqlcdb.Example{ID int64; Name string; CreatedAt time.Time; UpdatedAt time.Time}`
  - `(*Queries).CreateExample(ctx, name string) (Example, error)`
  - `(*Queries).CountExamples(ctx) (int64, error)`
  - `(*Queries).ListExamples(ctx, ListExamplesParams{Lim int64; Off int64}) ([]Example, error)`
  - make target：`make sqlc`、`make sqlc-verify`

- [ ] **Step 1: 写 `sqlc.yaml`**

```yaml
# sqlc config: migrations/ is the schema source of truth (goose annotations
# are understood natively), queries live next to the repository layer.
# Generated code is committed; `make sqlc-verify` guards drift.
version: "2"
sql:
  - engine: postgresql
    schema: migrations
    queries: internal/repository/queries
    gen:
      go:
        package: sqlcdb
        out: internal/repository/sqlcdb
        sql_package: pgx/v5
        overrides:
          # pgx/v5 defaults to pgtype.Timestamp[tz]; plain time.Time keeps
          # the model mapping trivial for NOT NULL columns.
          - db_type: pg_catalog.timestamp
            go_type: time.Time
          - db_type: pg_catalog.timestamptz
            go_type: time.Time
```

- [ ] **Step 2: 写 `internal/repository/queries/example.sql`**

```sql
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
```

- [ ] **Step 3: Makefile 加版本变量**

在 `OAPI_CODEGEN_VERSION  ?= v2.7.0` 下一行加：

```make
SQLC_VERSION          ?= v1.31.1
```

- [ ] **Step 4: Makefile 加 `_ensure-sqlc`**（放在 `_ensure-oapi-codegen` target 之后）

```make
.PHONY: _ensure-sqlc
_ensure-sqlc:
	@want="$(SQLC_VERSION)"; \
	if command -v sqlc >/dev/null 2>&1; then \
		got=$$(sqlc version 2>/dev/null); \
		if [ "$$got" = "$$want" ]; then \
			echo "sqlc $$got: ok"; exit 0; \
		fi; \
		echo "sqlc $$got != $$want, reinstalling..."; \
	else \
		echo "Installing sqlc $$want..."; \
	fi; \
	$(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@$$want
```

并在 `init:` 的 `_ensure-oapi-codegen` 行后加一行：

```make
	@$(MAKE) --no-print-directory _ensure-sqlc
```

- [ ] **Step 5: Makefile 加 `sqlc` / `sqlc-verify`**（放在 `oapi-verify` target 之后）

```make
SQLC_OUTPUT := internal/repository/sqlcdb

.PHONY: sqlc
sqlc: ## 从 migrations/ + internal/repository/queries/ 生成 internal/repository/sqlcdb
	@$(MAKE) --no-print-directory _ensure-sqlc
	sqlc generate
	@echo "generated: $(SQLC_OUTPUT)"

.PHONY: sqlc-verify
sqlc-verify: sqlc ## 校验 sqlc 生成产物与 SQL 同步且已提交（CI / 提交前用）
	@untracked=$$(git ls-files --others --exclude-standard -- $(SQLC_OUTPUT)); \
	if ! git diff --quiet -- $(SQLC_OUTPUT) || [ -n "$$untracked" ]; then \
		echo ""; \
		echo "ERROR: $(SQLC_OUTPUT) is out of sync with migrations/ + internal/repository/queries/."; \
		echo "       Run 'make sqlc' and commit the result."; \
		echo ""; \
		git --no-pager diff -- $(SQLC_OUTPUT) | head -40; \
		[ -z "$$untracked" ] || echo "untracked: $$untracked"; \
		exit 1; \
	fi
	@echo "sqlc-verify: $(SQLC_OUTPUT) is in sync."
```

- [ ] **Step 6: 把 `sqlc-verify` 并入 `verify`**

`verify:` 的 `## ...` 描述里在 `oapi-verify` 后插入 ` + sqlc-verify`；步骤列表在 `STEP=oapi-verify` 行之后加：

```make
	@$(MAKE) --no-print-directory _verify-step STEP=sqlc-verify
```

- [ ] **Step 7: `.golangci.yml` 排除生成目录**

`formatters.exclusions.paths` 与 `linters.exclusions.paths` 两处都在 `- internal/oapi` 下加：

```yaml
      - internal/repository/sqlcdb
```

同时把 formatters 那段注释改成：`# Generated code shouldn't be reformatted — make oapi / make sqlc own it.`

- [ ] **Step 8: 生成并整理依赖**

Run: `make sqlc && go mod tidy && go build ./...`
Expected: `internal/repository/sqlcdb/` 下出现 `db.go`、`models.go`、`example.sql.go`；`go.mod` 中 `github.com/jackc/pgx/v5` 变为 direct；build 成功。

核对生成签名与 Interfaces 段一致：

Run: `grep -n "^func (q \*Queries)\|^type ListExamplesParams" -A3 internal/repository/sqlcdb/example.sql.go | head -30`
Expected: `CreateExample(ctx context.Context, name string) (Example, error)`、`CountExamples(ctx context.Context) (int64, error)`、`ListExamplesParams` 字段为 `Lim int64` / `Off int64`。若不一致，修 SQL（不要手改生成代码）。

- [ ] **Step 9: 提交后跑全量校验**

```bash
git add sqlc.yaml internal/repository/queries/example.sql internal/repository/sqlcdb Makefile .golangci.yml go.mod go.sum
git commit -F - <<'EOF'
chore(build): 引入 sqlc 工具链与 example 查询

- 新增 sqlc.yaml：migrations/ 作 schema、internal/repository/queries/ 作查询、pgx/v5 输出到 internal/repository/sqlcdb
- 新增 example 的 CreateExample / CountExamples / ListExamples 查询并提交生成代码
- Makefile 新增 SQLC_VERSION=v1.31.1、_ensure-sqlc、sqlc、sqlc-verify，init 安装 sqlc，verify 链加入 sqlc-verify
- .golangci.yml 格式化与 lint 均排除 sqlcdb 生成目录
- go mod tidy：pgx/v5 转为直接依赖
EOF
make verify
```

Expected: `=== verify OK ===`（含 `STEP: sqlc-verify`）。失败则修复后 `git commit --amend`（本分支未 push，可 amend）。

- [ ] **Step 10: 验证 sqlc-verify 能抓漂移**

模拟"改了查询但没重新生成提交"：
```bash
printf '\n-- name: DriftProbe :one\nSELECT 1;\n' >> internal/repository/queries/example.sql
make sqlc-verify; echo "exit=$?"
```
Expected: 输出 `ERROR: internal/repository/sqlcdb is out of sync`，`exit=1`。
然后撤销这次临时改动（只涉及本步骤刚改的两个路径）：
```bash
git checkout -- internal/repository/queries/example.sql internal/repository/sqlcdb
make sqlc-verify
```
Expected: `sqlc-verify: internal/repository/sqlcdb is in sync.`

---

### Task 2: 数据库配置项改名

**Files:**
- Modify: `config/types.go:93-102`（`PostgresConfig`）
- Modify: `config/config.go:40-41, 89-96`
- Modify: `config/validate.go:32-39`
- Modify: `config/config_test.go:23-24, 49-52, 93, 113-114, 138, 149, 160, 170-171`
- Modify: `config/validate_test.go:55-77, 278-281`
- Modify: `.env.example:100-107`、`README.md:230`、`README_en.md:233`
- Modify: `internal/bootstrap/registry.go:73-85`（字段改名，临时适配旧 `database.Config`）

**Interfaces:**
- Produces: `config.PostgresConfig{DSN, LogLevel string; MaxConns, MinConns int; ConnMaxLifetime, ConnMaxIdleTime time.Duration}`（Task 3 的 `InitDatabase` 使用）

- [ ] **Step 1: 改测试（先红）**

`config/config_test.go`：
- 清空列表第 23-24 行改为：
```go
		"POSTGRES", "DB_LOG_LEVEL",
		"DB_MAX_CONNS", "DB_MIN_CONNS", "DB_CONN_MAX_LIFETIME", "DB_CONN_MAX_IDLE_TIME",
```
- 默认值断言第 49-50 行替换为：
```go
		{"Postgres.LogLevel", cfg.Postgres.LogLevel, "warn"},
		{"Postgres.MaxConns", cfg.Postgres.MaxConns, 30},
		{"Postgres.MinConns", cfg.Postgres.MinConns, 0},
```
- 其余出现 `DB_MAX_OPEN_CONNS` 的地方（93、138、149、160 行）全部改为 `DB_MAX_CONNS`；`cfg.Postgres.MaxOpenConns` 与报错文案里的 `MaxOpenConns`（113-114、170-171 行）改为 `MaxConns`。

`config/validate_test.go`：
- 第 278-281 行基准配置改为：
```go
		Postgres: PostgresConfig{
			DSN:      "postgres://x:y@localhost/db",
			LogLevel: "warn",
			MaxConns: 30,
		},
```
- 第 55-77 行三个 Postgres case 替换为：
```go
		{
			name: "Postgres DSN 非空时 MaxConns 必须正",
			mutate: func(c *Config) {
				c.Postgres.MaxConns = 0
			},
			wantErr:     true,
			wantInclude: "DB_MAX_CONNS",
		},
		{
			name: "Postgres DSN 非空时 MinConns 不能为负",
			mutate: func(c *Config) {
				c.Postgres.MinConns = -1
			},
			wantErr:     true,
			wantInclude: "DB_MIN_CONNS",
		},
		{
			name: "Postgres MinConns 不能超过 MaxConns",
			mutate: func(c *Config) {
				c.Postgres.MinConns = 31
			},
			wantErr:     true,
			wantInclude: "DB_MIN_CONNS",
		},
		{
			name: "Postgres LogLevel 非法",
			mutate: func(c *Config) {
				c.Postgres.LogLevel = "verbose"
			},
			wantErr:     true,
			wantInclude: "DB_LOG_LEVEL",
		},
		{
			name: "Postgres LogLevel 大小写不敏感",
			mutate: func(c *Config) {
				c.Postgres.LogLevel = "INFO"
			},
			wantErr: false,
		},
		{
			name: "Postgres DSN 为空时连接池约束跳过",
			mutate: func(c *Config) {
				c.Postgres.DSN = ""
				c.Postgres.MaxConns = 0 // invalid, but skipped when DSN is empty
				c.Postgres.LogLevel = "verbose"
			},
			wantErr: false,
		},
```

- [ ] **Step 2: 确认编译失败**

Run: `go test ./config/...`
Expected: FAIL，`unknown field MaxConns` / `MinConns`。

- [ ] **Step 3: 改 `config/types.go`**

```go
// PostgresConfig 是数据库连接配置。DSN 为空时所有 DB 相关功能均不启用，
// API 进程会 fail-fast 退出（DB 必需）；Worker 进程允许 DSN 为空（DB 可选）。
type PostgresConfig struct {
	DSN             string
	LogLevel        string
	MaxConns        int
	MinConns        int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}
```
（保留原注释原文，只替换字段。）

- [ ] **Step 4: 改 `config/config.go`**

第 40-41 行：
```go
			DSN:      os.Getenv("POSTGRES"),
			LogLevel: getEnvOrDefault("DB_LOG_LEVEL", "warn"),
```
第 89-92 行：
```go
	cfg.Postgres.MaxConns, err = intEnv("DB_MAX_CONNS", 30)
	collect(err)
	cfg.Postgres.MinConns, err = intEnv("DB_MIN_CONNS", 0)
	collect(err)
```

- [ ] **Step 5: 改 `config/validate.go`**

import 加 `"math"`。把第 32-39 行的 Postgres 块替换为一行调用：
```go
	validatePostgres(cfg.Postgres, add)
```
文件末尾新增：
```go
// validatePostgres checks pool and log settings only when a DSN is set;
// an empty DSN means the database module is disabled.
func validatePostgres(pg PostgresConfig, add func(string)) {
	if strings.TrimSpace(pg.DSN) == "" {
		return
	}
	if pg.MaxConns <= 0 || pg.MaxConns > math.MaxInt32 {
		add(fmt.Sprintf("DB_MAX_CONNS must be in [1, %d], got %d", math.MaxInt32, pg.MaxConns))
	}
	if pg.MinConns < 0 || pg.MinConns > pg.MaxConns {
		add(fmt.Sprintf("DB_MIN_CONNS must be in [0, DB_MAX_CONNS], got %d", pg.MinConns))
	}
	switch strings.ToLower(strings.TrimSpace(pg.LogLevel)) {
	case "silent", "error", "warn", "info":
	default:
		add(fmt.Sprintf("DB_LOG_LEVEL must be one of silent/error/warn/info, got %q", pg.LogLevel))
	}
}
```

- [ ] **Step 6: 临时适配 `internal/bootstrap/registry.go`**（Task 3 会重写 database.Config，这里只让编译通过）

```go
		MaxIdleConns:    cfg.Postgres.MinConns,
		MaxOpenConns:    cfg.Postgres.MaxConns,
```

- [ ] **Step 7: 跑测试**

Run: `go test ./config/... ./internal/bootstrap/...`
Expected: PASS

- [ ] **Step 8: 改 `.env.example`（第 100-107 行）**

```sh
# 数据库 SQL 日志级别：silent / error / warn / info。由 pgx tracer 输出，
# 只记 SQL 模板、不记参数值。生产推荐 warn（只记错误与 >200ms 慢查询）。
DB_LOG_LEVEL=warn

# pgxpool 连接池。生产请按实例规格 + Postgres max_connections 调整。
# DB_MAX_CONNS：池内最大连接数；DB_MIN_CONNS：常驻最小连接数（0 = 按需建连）。
DB_MAX_CONNS=30
DB_MIN_CONNS=0
DB_CONN_MAX_LIFETIME=30m
DB_CONN_MAX_IDLE_TIME=5m
```

- [ ] **Step 9: 改 README 两行**

`README.md:230`：
```markdown
- [ ] 根据实例规格和 Postgres `max_connections` 调 `DB_MAX_CONNS` / `DB_MIN_CONNS` / `DB_CONN_MAX_LIFETIME`，默认值（30 / 0 / 30m）是开发档位，不是生产档位。
```
`README_en.md:233`：
```markdown
- [ ] Tune `DB_MAX_CONNS` / `DB_MIN_CONNS` / `DB_CONN_MAX_LIFETIME` for your instance and Postgres `max_connections`. The defaults (30 / 0 / 30m) are development-tier, not production-tier.
```

- [ ] **Step 10: 全量校验并提交**

Run: `make verify`
Expected: `=== verify OK ===`（env-verify 确认 config 与 `.env.example` 同步）。

```bash
git add config/types.go config/config.go config/validate.go config/config_test.go config/validate_test.go .env.example README.md README_en.md internal/bootstrap/registry.go
git commit -F - <<'EOF'
refactor(bootstrap): 数据库配置项按 pgxpool 语义改名

- GORM_LOG_LEVEL → DB_LOG_LEVEL（默认 warn，DSN 非空时校验取值）
- DB_MAX_OPEN_CONNS → DB_MAX_CONNS，DB_MAX_IDLE_CONNS → DB_MIN_CONNS（默认 0，不得超过 DB_MAX_CONNS）
- Postgres 校验抽成 validatePostgres，降低 validate 圈复杂度
- 同步 .env.example、README 生产检查清单与测试
EOF
```

---

### Task 3: pkg/database 切到 pgxpool + QueryTracer

**Files:**
- Create: `pkg/database/tracer.go`、`pkg/database/tracer_test.go`、`pkg/database/database_test.go`
- Rewrite: `pkg/database/database.go`
- Delete: `pkg/database/gorm_logger.go`、`pkg/database/gorm_logger_test.go`
- Modify: `internal/bootstrap/registry.go:73-85`、`internal/bootstrap/api.go:32`
- Modify: `cmd/migrate/main.go:58-75`
- Modify: `internal/server_test.go:3-19, 42-73`

**Interfaces:**
- Consumes: `config.PostgresConfig`（Task 2）
- Produces:
  - `database.Config{DSN, LogLevel string; MaxConns, MinConns int; ConnMaxLifetime, ConnMaxIdleTime time.Duration}`
  - `database.Init(ctx context.Context, cfg Config) (*DBManager, error)`
  - `database.NewManager(pool *pgxpool.Pool) (*DBManager, error)`（PR1 签名；Task 5 改为不返回 error）
  - `(*DBManager).Pool() *pgxpool.Pool`、`SQLDB() *sql.DB`、`DB() *gorm.DB`（仅 PR1）、`Ping(ctx) error`、`Close() error`
  - 包内：`queryTracer{level logLevel}`、`parseLogLevel(string) (logLevel, error)`、`traceKey{}`、`traceData{start time.Time; sql string}`

- [ ] **Step 1: 删旧 logger 及其测试**

```bash
git rm -q pkg/database/gorm_logger.go pkg/database/gorm_logger_test.go
```

- [ ] **Step 2: 写 `pkg/database/tracer_test.go`（先红）**

```go
package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	applog "go-skeleton/pkg/log"
)

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    logLevel
		wantErr bool
	}{
		{"silent", logSilent, false},
		{"error", logError, false},
		{"", logWarn, false},
		{"WARN", logWarn, false},
		{" info ", logInfo, false},
		{"verbose", logWarn, true},
	}
	for _, c := range cases {
		got, err := parseLogLevel(c.in)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("parseLogLevel(%q) = (%v, %v), want (%v, err=%v)", c.in, got, err, c.want, c.wantErr)
		}
	}
}

func TestQueryTracerStart(t *testing.T) {
	data := pgx.TraceQueryStartData{SQL: "SELECT 1"}

	silent := &queryTracer{level: logSilent}
	if ctx := silent.TraceQueryStart(t.Context(), nil, data); ctx.Value(traceKey{}) != nil {
		t.Fatal("silent tracer should not attach trace data")
	}

	warn := &queryTracer{level: logWarn}
	td, ok := warn.TraceQueryStart(t.Context(), nil, data).Value(traceKey{}).(traceData)
	if !ok || td.sql != "SELECT 1" || td.start.IsZero() {
		t.Fatalf("trace data = %+v, ok=%v", td, ok)
	}
}

func TestQueryTracerEnd(t *testing.T) {
	fast := time.Now()
	slow := time.Now().Add(-2 * slowQueryThreshold)
	boom := errors.New("boom")
	canceled := errors.Join(errors.New("query"), context.Canceled)
	cases := []struct {
		name      string
		level     logLevel
		start     time.Time
		err       error
		wantLevel zapcore.Level
		wantMsg   string // empty means nothing should be logged
	}{
		{"error logged at warn", logWarn, fast, boom, zapcore.ErrorLevel, "db query failed"},
		{"error logged at error", logError, fast, boom, zapcore.ErrorLevel, "db query failed"},
		{"slow error still error", logWarn, slow, boom, zapcore.ErrorLevel, "db query failed"},
		{"canceled downgraded to warn", logWarn, fast, canceled, zapcore.WarnLevel, "db query canceled"},
		{"canceled hidden at error", logError, fast, canceled, 0, ""},
		{"slow query warned", logWarn, slow, nil, zapcore.WarnLevel, "db slow query"},
		{"fast query skipped at warn", logWarn, fast, nil, 0, ""},
		{"all queries at info", logInfo, fast, nil, zapcore.InfoLevel, "db query"},
		{"silent drops errors", logSilent, fast, boom, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			defer applog.SetLogger(zap.New(core))()

			ctx := applog.WithTraceID(t.Context(), "trace-db")
			ctx = context.WithValue(ctx, traceKey{}, traceData{start: c.start, sql: "SELECT $1"})
			tr := &queryTracer{level: c.level}
			tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1"), Err: c.err})

			entries := logs.All()
			if c.wantMsg == "" {
				if len(entries) != 0 {
					t.Fatalf("expected no log, got %q", entries[0].Message)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("got %d log entries, want 1", len(entries))
			}
			e := entries[0]
			if e.Level != c.wantLevel || e.Message != c.wantMsg {
				t.Errorf("got (%v, %q), want (%v, %q)", e.Level, e.Message, c.wantLevel, c.wantMsg)
			}
			fields := e.ContextMap()
			if fields["trace_id"] != "trace-db" || fields["sql"] != "SELECT $1" || fields["rows"] != int64(1) {
				t.Errorf("unexpected fields: %v", fields)
			}
		})
	}
}

func TestQueryTracerEndWithoutStartIsNoop(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	defer applog.SetLogger(zap.New(core))()

	(&queryTracer{level: logInfo}).TraceQueryEnd(t.Context(), nil, pgx.TraceQueryEndData{})
	if logs.Len() != 0 {
		t.Fatalf("expected no log without start data, got %d", logs.Len())
	}
}
```

- [ ] **Step 3: 写 `pkg/database/database_test.go`（先红）**

```go
package database

import (
	"testing"
	"time"
)

// unreachableDSN points at a closed port: pgxpool connects lazily, so
// constructing a pool against it must still succeed.
const unreachableDSN = "postgres://u:p@127.0.0.1:1/db?sslmode=disable"

func TestNormalizePoolSettings(t *testing.T) {
	cases := []struct {
		name string
		in   Config
		want poolSettings
	}{
		{"zero uses defaults", Config{}, poolSettings{maxConns: 30, connMaxLifetime: 30 * time.Minute, connMaxIdleTime: 5 * time.Minute}},
		{"custom values kept", Config{MaxConns: 50, MinConns: 5, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Minute}, poolSettings{maxConns: 50, minConns: 5, connMaxLifetime: time.Hour, connMaxIdleTime: time.Minute}},
		{"negative falls back", Config{MaxConns: -1, MinConns: -1}, poolSettings{maxConns: 30, connMaxLifetime: 30 * time.Minute, connMaxIdleTime: 5 * time.Minute}},
		{"min above max dropped", Config{MaxConns: 10, MinConns: 11}, poolSettings{maxConns: 10, connMaxLifetime: 30 * time.Minute, connMaxIdleTime: 5 * time.Minute}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizePoolSettings(c.in); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestInitEmptyDSNReturnsEmptyManager(t *testing.T) {
	m, err := Init(t.Context(), Config{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.Pool() != nil || m.SQLDB() != nil {
		t.Fatal("empty DSN should yield a manager without pool")
	}
	if err := m.Ping(t.Context()); err == nil {
		t.Fatal("Ping on empty manager should fail")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestInitRejectsInvalidLogLevel(t *testing.T) {
	if _, err := Init(t.Context(), Config{DSN: unreachableDSN, LogLevel: "verbose"}); err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestInitBuildsLazyPool(t *testing.T) {
	m, err := Init(t.Context(), Config{DSN: unreachableDSN, MaxConns: 7})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	cfg := m.Pool().Config()
	if cfg.MaxConns != 7 {
		t.Errorf("MaxConns = %d, want 7", cfg.MaxConns)
	}
	if _, ok := cfg.ConnConfig.Tracer.(*queryTracer); !ok {
		t.Errorf("tracer = %T, want *queryTracer", cfg.ConnConfig.Tracer)
	}
	if m.SQLDB() == nil {
		t.Error("SQLDB should share the pool")
	}
}

func TestNilManagerIsSafe(t *testing.T) {
	var m *DBManager
	if m.Pool() != nil || m.SQLDB() != nil {
		t.Fatal("nil manager accessors should return nil")
	}
	if err := m.Ping(t.Context()); err == nil {
		t.Fatal("Ping on nil manager should fail")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close on nil manager: %v", err)
	}
}
```

- [ ] **Step 4: 确认失败**

Run: `go test ./pkg/database/...`
Expected: FAIL（`undefined: logLevel` / `queryTracer` 等）。

- [ ] **Step 5: 写 `pkg/database/tracer.go`**

```go
package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	applog "go-skeleton/pkg/log"
)

// slowQueryThreshold marks queries slower than this as warn.
const slowQueryThreshold = 200 * time.Millisecond

// logLevel mirrors DB_LOG_LEVEL; higher values log more.
type logLevel int

const (
	logSilent logLevel = iota
	logError
	logWarn
	logInfo
)

// parseLogLevel maps DB_LOG_LEVEL to logLevel. Empty means warn. Unknown
// values are rejected here as well as in config.validate, so callers that
// bypass config still fail loudly.
func parseLogLevel(level string) (logLevel, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "silent":
		return logSilent, nil
	case "error":
		return logError, nil
	case "warn", "":
		return logWarn, nil
	case "info":
		return logInfo, nil
	default:
		return logWarn, fmt.Errorf("unknown db log level %q (want silent/error/warn/info)", level)
	}
}

type traceKey struct{}

type traceData struct {
	start time.Time
	sql   string
}

// queryTracer implements pgx.QueryTracer and routes SQL logs through
// applog.FromContext so they carry trace_id. Only the SQL template is
// logged, never argument values, to keep sensitive data out of logs.
type queryTracer struct {
	level logLevel
}

var _ pgx.QueryTracer = (*queryTracer)(nil)

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if t.level == logSilent {
		return ctx
	}
	return context.WithValue(ctx, traceKey{}, traceData{start: time.Now(), sql: data.SQL})
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	td, ok := ctx.Value(traceKey{}).(traceData)
	if !ok {
		return
	}
	elapsed := time.Since(td.start)

	var (
		need  logLevel
		level zapcore.Level
		msg   string
		extra zap.Field
	)
	switch {
	case errors.Is(data.Err, context.Canceled):
		// Client went away; not a database fault.
		need, level, msg, extra = logWarn, zapcore.WarnLevel, "db query canceled", zap.Error(data.Err)
	case data.Err != nil:
		need, level, msg, extra = logError, zapcore.ErrorLevel, "db query failed", zap.Error(data.Err)
	case elapsed > slowQueryThreshold:
		need, level, msg, extra = logWarn, zapcore.WarnLevel, "db slow query", zap.Duration("threshold", slowQueryThreshold)
	default:
		need, level, msg, extra = logInfo, zapcore.InfoLevel, "db query", zap.Skip()
	}
	if t.level < need {
		return
	}
	applog.FromContext(ctx).Log(level, msg,
		zap.String("sql", td.sql),
		zap.Int64("rows", data.CommandTag.RowsAffected()),
		zap.Duration("elapsed", elapsed),
		extra,
	)
}
```

- [ ] **Step 6: 重写 `pkg/database/database.go`**

```go
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	applog "go-skeleton/pkg/log"
)

var errNotConfigured = errors.New("database is not configured")

// DBManager owns the Postgres connection pool. pool is the only real pool;
// sqlDB is a database/sql view over the same pool for libraries that need
// *sql.DB (goose).
type DBManager struct {
	pool  *pgxpool.Pool
	sqlDB *sql.DB
	// gorm is a temporary bridge sharing sqlDB until repositories move to
	// sqlc; it is removed afterwards.
	gorm *gorm.DB
}

// Config is the database connection config: DSN + pool settings.
type Config struct {
	DSN             string
	LogLevel        string
	MaxConns        int
	MinConns        int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// poolSettings is Config after defaults are applied.
type poolSettings struct {
	maxConns        int32
	minConns        int32
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration
}

// Init builds the pool. An empty DSN yields an empty manager (no error) so
// InitAPI / InitWorker decide whether the database is required.
//
// pgxpool connects lazily and Init does not ping: startup fail-fast is
// covered by bootstrap.probeDependencies (API / Worker) and the explicit
// ping in cmd/migrate.
func Init(ctx context.Context, cfg Config) (*DBManager, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return &DBManager{}, nil
	}
	poolCfg, err := newPoolConfig(cfg)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	m, err := NewManager(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	applog.L().Info("postgres pool created")
	return m, nil
}

// NewManager wraps an existing pool. Tests can pass a pool pointing at an
// unreachable address because pgxpool does not connect until first use.
func NewManager(pool *pgxpool.Pool) (*DBManager, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:               logger.Discard, // SQL is logged by queryTracer
		DisableAutomaticPing: true,
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open gorm on shared pool: %w", err)
	}
	return &DBManager{pool: pool, sqlDB: sqlDB, gorm: gdb}, nil
}

// Pool returns the pgx pool. Only repository wiring should use it.
func (m *DBManager) Pool() *pgxpool.Pool {
	if m == nil {
		return nil
	}
	return m.pool
}

// SQLDB returns a database/sql handle backed by the same pool.
func (m *DBManager) SQLDB() *sql.DB {
	if m == nil {
		return nil
	}
	return m.sqlDB
}

// DB returns the temporary GORM bridge.
func (m *DBManager) DB() *gorm.DB {
	if m == nil {
		return nil
	}
	return m.gorm
}

// Ping checks reachability; /health calls it with a short-timeout ctx.
func (m *DBManager) Ping(ctx context.Context) error {
	if m == nil || m.pool == nil {
		return errNotConfigured
	}
	if err := m.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close closes sqlDB first (it does not own the pool), then the pool.
// nil-safe; bootstrap.Registry.Close calls it.
func (m *DBManager) Close() error {
	if m == nil || m.pool == nil {
		return nil
	}
	err := m.sqlDB.Close()
	m.pool.Close()
	return err
}

func newPoolConfig(cfg Config) (*pgxpool.Config, error) {
	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		return nil, err
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	s := normalizePoolSettings(cfg)
	poolCfg.MaxConns = s.maxConns
	poolCfg.MinConns = s.minConns
	poolCfg.MaxConnLifetime = s.connMaxLifetime
	poolCfg.MaxConnIdleTime = s.connMaxIdleTime
	poolCfg.ConnConfig.Tracer = &queryTracer{level: level}
	return poolCfg, nil
}

// normalizePoolSettings applies defaults for zero / out-of-range values so
// callers need not fill every field.
func normalizePoolSettings(cfg Config) poolSettings {
	s := poolSettings{
		maxConns:        30,
		connMaxLifetime: 30 * time.Minute,
		connMaxIdleTime: 5 * time.Minute,
	}
	if cfg.MaxConns > 0 && cfg.MaxConns <= math.MaxInt32 {
		s.maxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 && cfg.MinConns <= int(s.maxConns) {
		s.minConns = int32(cfg.MinConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		s.connMaxLifetime = cfg.ConnMaxLifetime
	}
	if cfg.ConnMaxIdleTime > 0 {
		s.connMaxIdleTime = cfg.ConnMaxIdleTime
	}
	return s
}
```

- [ ] **Step 7: 跑 database 测试**

Run: `go test ./pkg/database/... -v -run 'Test(ParseLogLevel|QueryTracer|NormalizePool|Init|NilManager)'`
Expected: PASS

- [ ] **Step 8: 改 `internal/bootstrap/registry.go`**

import 加 `"context"`；`InitDatabase` 替换为：
```go
// InitDatabase translates cfg into database.Config and builds the pgx pool.
// An empty DSN yields a manager with a nil pool; callers (InitAPI /
// InitWorker / cmd/migrate) decide whether that is fatal.
func InitDatabase(cfg *config.Config) (*database.DBManager, error) {
	return database.Init(context.Background(), database.Config{
		DSN:             cfg.Postgres.DSN,
		LogLevel:        cfg.Postgres.LogLevel,
		MaxConns:        cfg.Postgres.MaxConns,
		MinConns:        cfg.Postgres.MinConns,
		ConnMaxLifetime: cfg.Postgres.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Postgres.ConnMaxIdleTime,
	})
}
```

- [ ] **Step 9: 改 `internal/bootstrap/api.go:32`**

```go
	if dbMgr.Pool() == nil {
```

- [ ] **Step 10: 改 `cmd/migrate/main.go:67-75`**

把 `if dbMgr.DB() == nil {` 到 `sqlDB, err := ... }` 这段替换为：
```go
	sqlDB := dbMgr.SQLDB()
	if sqlDB == nil {
		applog.L().Fatal("database is not configured")
	}
	// sqlDB is a database/sql view over the shared pgx pool; goose reuses it
	// instead of opening its own connections.
```
（`sqlDB` 之后传给 `goose.NewProvider` 的代码不变。）

- [ ] **Step 11: 改 `internal/server_test.go`**

import 去掉 `gorm.io/driver/postgres`、`gorm.io/gorm`，加 `"github.com/jackc/pgx/v5/pgxpool"`。`testRegistryForServer` 里原 `gorm.Open(...)` 段（第 45-52 行）替换为：
```go
	pool, err := pgxpool.New(t.Context(), "postgres://u:p@127.0.0.1:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	dbMgr, err := database.NewManager(pool)
	if err != nil {
		t.Fatalf("database.NewManager: %v", err)
	}
	t.Cleanup(func() { _ = dbMgr.Close() })
```
第 72 行 `DB: database.NewTestManager(db),` 改为 `DB: dbMgr,`。

- [ ] **Step 12: 全量校验**

Run: `go mod tidy && make verify`
Expected: `=== verify OK ===`。`grep -rn "NewTestManager\|GORM_LOG_LEVEL\|MaxOpenConns\|MaxIdleConns" --include='*.go' .` 无输出。

- [ ] **Step 13: 本地端到端冒烟（需要 docker）**

Run:
```bash
make dev-up && go run ./cmd/migrate && (DB_LOG_LEVEL=info timeout 8 go run ./cmd/api 2>&1 | grep -m3 '"db query"\|postgres pool created' ; true)
```
Expected: 看到 `postgres pool created`，迁移成功；`DB_LOG_LEVEL=info` 时有 `db query` 日志且 `sql` 字段不含参数值。若本机无 docker，记录"未做端到端冒烟"并在 PR 描述里说明。

- [ ] **Step 14: 提交**

```bash
git add pkg/database/database.go pkg/database/tracer.go pkg/database/tracer_test.go pkg/database/database_test.go internal/bootstrap/registry.go internal/bootstrap/api.go cmd/migrate/main.go internal/server_test.go go.mod go.sum
git commit -F - <<'EOF'
refactor(database): 连接池切到 pgxpool，SQL 日志改用 pgx tracer

- DBManager 持有 pgxpool.Pool，SQLDB() 基于同一池提供 *sql.DB 给 goose
- GORM 临时通过 stdlib.OpenDBFromPool 复用同一连接池，repository 切换后删除
- 新增 queryTracer：只记 SQL 模板，出错记 error、慢于 200ms 记 warn、Canceled 降级 warn
- 删除 zapGormLogger；NewTestManager 由 NewManager(pool) 取代
- cmd/migrate 改用 SQLDB()；api 以 Pool() 判定 DB 是否配置
EOF
```

- [ ] **Step 15: PR1 收尾**

Run: `make verify GO_TEST_FLAGS=-race && make scaffold-verify && make sec`
Expected: 全绿。随后**停下来询问用户**是否 push 并开 PR1（标题 `refactor: sqlc 迁移 PR1 — 基础设施`）。PR1 合并后再开始 PR2。

---

# PR2：切换到 sqlc 并删除 GORM（分支 `refactor/sqlc-switch`，从最新 master 拉出）

### Task 4: repository 切换到 sqlc（事务、example、model、调用方）

**Files:**
- Rewrite: `internal/repository/tx.go`
- Create: `internal/repository/tx_test.go`（含本包共享 mock）
- Rewrite: `internal/repository/example.go`、`internal/repository/example_test.go`、`internal/repository/example_integration_test.go`
- Modify: `internal/model/example.go`；Delete: `internal/model/example_test.go`
- Create: `internal/service/tx.go`
- Modify: `internal/service/example.go:141`
- Modify: `internal/server.go:257, 268`（并加 Transactor 断言）、`internal/worker.go:98, 117`

**Interfaces:**
- Consumes: Task 1 的 sqlcdb API；Task 3 的 `(*DBManager).Pool()`
- Produces:
  - `repository.DB interface { sqlcdb.DBTX; BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) }`
  - `repository.WithTx(ctx, tx sqlcdb.DBTX) context.Context`
  - `repository.InTx(ctx, db DB, fn func(context.Context) error) error`
  - `repository.InTxWithOptions(ctx, db DB, opts *sql.TxOptions, fn func(context.Context) error) error`
  - `repository.TxManager`、`repository.NewTxManager(db DB) *TxManager`、`(*TxManager).InTx`、`(*TxManager).InTxWithOptions`
  - `repository.NewExampleRepository(db DB) *ExampleRepository`
  - `service.Transactor`（`InTx` / `InTxWithOptions`，签名同 TxManager 方法）
  - `model.Example{ID int64; Name string; CreatedAt, UpdatedAt time.Time}`（仅 json tag）
  - 包内测试 mock：`mockDBTX`、`mockDB`、`mockTx`、`mockRow`、`mockRows`（Task 6 的脚手架模板引用 `mockDBTX` 名字）

- [ ] **Step 1: 写 `internal/repository/tx_test.go`（先红）**

```go
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---------------------------------------------------------------------------
// Shared hand-written mocks for this package. They live here (not in
// example_test.go) so they survive `make drop-example`.

// mockDBTX implements sqlcdb.DBTX with per-method func fields.
type mockDBTX struct {
	execFunc     func(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)
	queryFunc    func(ctx context.Context, query string, args ...any) (pgx.Rows, error)
	queryRowFunc func(ctx context.Context, query string, args ...any) pgx.Row
}

func (m *mockDBTX) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return m.execFunc(ctx, query, args...)
}

func (m *mockDBTX) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return m.queryFunc(ctx, query, args...)
}

func (m *mockDBTX) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return m.queryRowFunc(ctx, query, args...)
}

// mockDB implements DB: a DBTX that can also begin transactions.
type mockDB struct {
	mockDBTX
	beginFunc func(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func (m *mockDB) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return m.beginFunc(ctx, opts)
}

// mockTx embeds a nil pgx.Tx: any method not overridden below panics,
// surfacing unexpected calls instead of silently passing.
type mockTx struct {
	pgx.Tx
	dbtx       *mockDBTX
	committed  bool
	rolledBack bool
}

func (m *mockTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return m.dbtx.Exec(ctx, query, args...)
}

func (m *mockTx) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return m.dbtx.Query(ctx, query, args...)
}

func (m *mockTx) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return m.dbtx.QueryRow(ctx, query, args...)
}

func (m *mockTx) Commit(context.Context) error {
	m.committed = true
	return nil
}

// Rollback mimics pgx: after commit/rollback it returns ErrTxClosed, which
// pgx.BeginTxFunc's deferred rollback ignores.
func (m *mockTx) Rollback(context.Context) error {
	if m.committed || m.rolledBack {
		return pgx.ErrTxClosed
	}
	m.rolledBack = true
	return nil
}

// mockRow implements pgx.Row.
type mockRow struct {
	values []any
	err    error
}

func (r mockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return assign(dest, r.values)
}

// mockRows implements the pgx.Rows subset sqlc-generated :many code uses
// (Next / Scan / Close / Err); the embedded nil interface panics otherwise.
type mockRows struct {
	pgx.Rows
	data [][]any
	pos  int
}

func (r *mockRows) Next() bool {
	r.pos++
	return r.pos <= len(r.data)
}

func (r *mockRows) Scan(dest ...any) error { return assign(dest, r.data[r.pos-1]) }
func (r *mockRows) Close()                 {}
func (r *mockRows) Err() error             { return nil }

// assign copies values into scan destinations (test-only reflection).
func assign(dest, values []any) error {
	if len(dest) != len(values) {
		return fmt.Errorf("scan: %d destinations, %d values", len(dest), len(values))
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(values[i]))
	}
	return nil
}

// newBeginDB returns a DB whose BeginTx hands out tx and records opts.
func newBeginDB(tx *mockTx, gotOpts *pgx.TxOptions) *mockDB {
	return &mockDB{beginFunc: func(_ context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
		if gotOpts != nil {
			*gotOpts = opts
		}
		return tx, nil
	}}
}

// TestRowMocksScan keeps the shared row mocks honest, and keeps them in
// use after `make drop-example` deletes example_test.go (otherwise the
// unused linter fails).
func TestRowMocksScan(t *testing.T) {
	var (
		id   int64
		name string
	)
	if err := (mockRow{values: []any{int64(1), "a"}}).Scan(&id, &name); err != nil || id != 1 || name != "a" {
		t.Fatalf("mockRow scan: id=%d name=%q err=%v", id, name, err)
	}
	if err := (mockRow{values: []any{int64(1)}}).Scan(&id, &name); err == nil {
		t.Fatal("expected arity mismatch error")
	}

	rows := &mockRows{data: [][]any{{int64(2)}, {int64(3)}}}
	var got []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("mockRows scan: %v", err)
		}
		got = append(got, v)
	}
	rows.Close()
	if rows.Err() != nil || !slices.Equal(got, []int64{2, 3}) {
		t.Fatalf("mockRows = %v, want [2 3]", got)
	}
}

// ---------------------------------------------------------------------------
// tx.go tests

func TestWithTxAndDBFromContext(t *testing.T) {
	base := &mockDB{}
	tx := &mockDBTX{}

	if got := dbFromContext(WithTx(t.Context(), tx), base); got != tx {
		t.Fatalf("expected tx from context, got %#v", got)
	}
	if got := dbFromContext(t.Context(), base); got != base {
		t.Fatalf("expected fallback base db, got %#v", got)
	}
}

// TestInTxNilArgs: fn=nil wins over db=nil (the more specific complaint).
func TestInTxNilArgs(t *testing.T) {
	if err := InTx(t.Context(), nil, nil); !errors.Is(err, errNilTxFn) {
		t.Fatalf("fn=nil err = %v, want errNilTxFn", err)
	}
	if err := InTx(t.Context(), nil, func(context.Context) error { return nil }); !errors.Is(err, errNilDB) {
		t.Fatalf("db=nil err = %v, want errNilDB", err)
	}
	if err := InTxWithOptions(t.Context(), nil, &sql.TxOptions{ReadOnly: true}, nil); !errors.Is(err, errNilTxFn) {
		t.Fatalf("WithOptions fn=nil err = %v, want errNilTxFn", err)
	}
}

func TestInTxCommitsOnSuccess(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{}}
	db := newBeginDB(tx, nil)

	err := InTx(t.Context(), db, func(txCtx context.Context) error {
		if got := dbFromContext(txCtx, db); got != tx {
			t.Fatalf("txCtx should carry the tx, got %#v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("committed=%v rolledBack=%v, want commit only", tx.committed, tx.rolledBack)
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{}}
	want := errors.New("biz boom")

	err := InTx(t.Context(), newBeginDB(tx, nil), func(context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("committed=%v rolledBack=%v, want rollback only", tx.committed, tx.rolledBack)
	}
}

func TestInTxRollsBackAndRepanicsOnPanic(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{}}
	defer func() {
		if r := recover(); r != "boom" {
			t.Fatalf("recover = %v, want boom", r)
		}
		if tx.committed || !tx.rolledBack {
			t.Fatalf("committed=%v rolledBack=%v, want rollback only", tx.committed, tx.rolledBack)
		}
	}()
	_ = InTx(t.Context(), newBeginDB(tx, nil), func(context.Context) error { panic("boom") })
	t.Fatal("InTx should re-panic")
}

func TestInTxPropagatesBeginError(t *testing.T) {
	want := errors.New("begin failed")
	db := &mockDB{beginFunc: func(context.Context, pgx.TxOptions) (pgx.Tx, error) { return nil, want }}
	if err := InTx(t.Context(), db, func(context.Context) error { return nil }); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// TestInTxReusesActiveTransaction: nested calls reuse the outer tx and
// ignore opts (isolation can only be set at BEGIN).
func TestInTxReusesActiveTransaction(t *testing.T) {
	outer := &mockDBTX{}
	ctx := WithTx(t.Context(), outer)
	db := &mockDB{beginFunc: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		t.Fatal("nested call must not begin a new transaction")
		return nil, nil
	}}

	called := 0
	for _, opts := range []*sql.TxOptions{nil, {Isolation: sql.LevelSerializable, ReadOnly: true}} {
		err := InTxWithOptions(ctx, db, opts, func(inner context.Context) error {
			called++
			if got := dbFromContext(inner, db); got != outer {
				t.Fatalf("expected reused outer tx, got %#v", got)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("nested InTxWithOptions: %v", err)
		}
	}
	if called != 2 {
		t.Fatalf("fn called %d times, want 2", called)
	}
}

func TestToPgxTxOptions(t *testing.T) {
	cases := []struct {
		name    string
		in      *sql.TxOptions
		want    pgx.TxOptions
		wantErr bool
	}{
		{"nil", nil, pgx.TxOptions{}, false},
		{"default", &sql.TxOptions{}, pgx.TxOptions{}, false},
		{"read uncommitted", &sql.TxOptions{Isolation: sql.LevelReadUncommitted}, pgx.TxOptions{IsoLevel: pgx.ReadUncommitted}, false},
		{"read committed", &sql.TxOptions{Isolation: sql.LevelReadCommitted}, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, false},
		{"repeatable read only", &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, false},
		{"serializable", &sql.TxOptions{Isolation: sql.LevelSerializable}, pgx.TxOptions{IsoLevel: pgx.Serializable}, false},
		{"snapshot unsupported", &sql.TxOptions{Isolation: sql.LevelSnapshot}, pgx.TxOptions{}, true},
		{"linearizable unsupported", &sql.TxOptions{Isolation: sql.LevelLinearizable}, pgx.TxOptions{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := toPgxTxOptions(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestInTxUnsupportedIsolationDoesNotBegin(t *testing.T) {
	db := &mockDB{beginFunc: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		t.Fatal("BeginTx must not be called for unsupported isolation")
		return nil, nil
	}}
	err := InTxWithOptions(t.Context(), db, &sql.TxOptions{Isolation: sql.LevelSnapshot}, func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected error for unsupported isolation")
	}
}

func TestTxManagerDelegates(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{}}
	var gotOpts pgx.TxOptions
	m := NewTxManager(newBeginDB(tx, &gotOpts))

	if err := m.InTx(t.Context(), func(context.Context) error { return nil }); err != nil || !tx.committed {
		t.Fatalf("InTx err=%v committed=%v", err, tx.committed)
	}

	tx2 := &mockTx{dbtx: &mockDBTX{}}
	m = NewTxManager(newBeginDB(tx2, &gotOpts))
	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	if err := m.InTxWithOptions(t.Context(), opts, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("InTxWithOptions: %v", err)
	}
	if want := (pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}); gotOpts != want {
		t.Fatalf("opts = %+v, want %+v", gotOpts, want)
	}
}
```

- [ ] **Step 2: 重写 `internal/repository/example_test.go`（先红）**

```go
package repository

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go-skeleton/internal/model"
)

func TestExampleRepositoryCreateUsesTransactionFromContext(t *testing.T) {
	now := time.Now()
	var gotSQL string
	var gotArgs []any
	txDBTX := &mockDBTX{queryRowFunc: func(_ context.Context, query string, args ...any) pgx.Row {
		gotSQL, gotArgs = query, args
		return mockRow{values: []any{int64(7), "example", now, now}}
	}}
	base := &mockDB{mockDBTX: mockDBTX{queryRowFunc: func(context.Context, string, ...any) pgx.Row {
		t.Fatal("base db must not be used inside a transaction")
		return nil
	}}}

	example := &model.Example{Name: "example"}
	if err := NewExampleRepository(base).Create(WithTx(t.Context(), txDBTX), example); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(gotSQL, "INSERT INTO examples") {
		t.Fatalf("unexpected SQL: %q", gotSQL)
	}
	if !slices.Equal(gotArgs, []any{"example"}) {
		t.Fatalf("args = %#v, want [example]", gotArgs)
	}
	if example.ID != 7 || !example.CreatedAt.Equal(now) || !example.UpdatedAt.Equal(now) {
		t.Fatalf("example not populated from RETURNING: %+v", example)
	}
}

func TestExampleRepositoryCreatePropagatesError(t *testing.T) {
	want := errors.New("insert failed")
	db := &mockDB{mockDBTX: mockDBTX{queryRowFunc: func(context.Context, string, ...any) pgx.Row {
		return mockRow{err: want}
	}}}
	if err := NewExampleRepository(db).Create(t.Context(), &model.Example{Name: "x"}); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestExampleRepositoryListRunsInReadOnlySnapshot(t *testing.T) {
	now := time.Now()
	var listSQL string
	var listArgs []any
	tx := &mockTx{dbtx: &mockDBTX{
		queryRowFunc: func(_ context.Context, query string, _ ...any) pgx.Row {
			if !strings.Contains(query, "count(*)") {
				t.Errorf("expected count query, got %q", query)
			}
			return mockRow{values: []any{int64(2)}}
		},
		queryFunc: func(_ context.Context, query string, args ...any) (pgx.Rows, error) {
			listSQL, listArgs = query, args
			return &mockRows{data: [][]any{
				{int64(2), "b", now, now},
				{int64(1), "a", now, now},
			}}, nil
		},
	}}
	var gotOpts pgx.TxOptions

	examples, total, err := NewExampleRepository(newBeginDB(tx, &gotOpts)).List(t.Context(), 10, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := (pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}); gotOpts != want {
		t.Fatalf("tx opts = %+v, want %+v", gotOpts, want)
	}
	if !tx.committed {
		t.Fatal("read-only tx should be committed")
	}
	if total != 2 || len(examples) != 2 || examples[0].ID != 2 || examples[1].Name != "a" {
		t.Fatalf("total=%d examples=%+v", total, examples)
	}
	for _, frag := range []string{"ORDER BY id DESC", "LIMIT", "OFFSET"} {
		if !strings.Contains(listSQL, frag) {
			t.Fatalf("list SQL missing %q: %q", frag, listSQL)
		}
	}
	if !slices.Equal(listArgs, []any{int64(10), int64(3)}) {
		t.Fatalf("list args = %#v, want [10 3] as int64", listArgs)
	}
}

func TestExampleRepositoryListEmptyReturnsNonNilSlice(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{
		queryRowFunc: func(context.Context, string, ...any) pgx.Row { return mockRow{values: []any{int64(0)}} },
		queryFunc:    func(context.Context, string, ...any) (pgx.Rows, error) { return &mockRows{}, nil },
	}}
	examples, total, err := NewExampleRepository(newBeginDB(tx, nil)).List(t.Context(), 20, 0)
	if err != nil || total != 0 || examples == nil || len(examples) != 0 {
		t.Fatalf("examples=%#v total=%d err=%v, want empty non-nil slice", examples, total, err)
	}
}
```

- [ ] **Step 3: 确认失败**

Run: `go test ./internal/repository/...`
Expected: FAIL（`mockDB` 不满足 `*gorm.DB` / `undefined: toPgxTxOptions` 等编译错误）。

- [ ] **Step 4: 重写 `internal/repository/tx.go`**

```go
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go-skeleton/internal/repository/sqlcdb"
)

// DB is what repositories need from the database: run queries (sqlc's
// DBTX) and begin transactions. *pgxpool.Pool satisfies it.
type DB interface {
	sqlcdb.DBTX
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// txKey is the private context key for the active transaction handle.
type txKey struct{}

var (
	errNilDB   = errors.New("repository: database connection is required")
	errNilTxFn = errors.New("repository: transaction callback is required")
)

// WithTx attaches an active transaction to ctx so dbFromContext can reuse
// it across repositories. Normally only InTx calls it.
func WithTx(ctx context.Context, tx sqlcdb.DBTX) context.Context {
	return context.WithValue(normalizeContext(ctx), txKey{}, tx)
}

// InTx runs fn in a transaction with the default isolation (READ
// COMMITTED on Postgres). If ctx already carries a transaction (nested
// call) it is reused instead of opening a SAVEPOINT.
//
// Services wrap multi-repository calls in InTx; repositories never call it
// for cross-repository work and only read the handle via dbFromContext.
func InTx(ctx context.Context, db DB, fn func(context.Context) error) error {
	return InTxWithOptions(ctx, db, nil, fn)
}

// InTxWithOptions runs fn in a transaction with custom isolation /
// read-only mode. Nested calls reuse the outer transaction and ignore
// opts: isolation can only be set at BEGIN, so decide it at the outermost
// call.
//
// Commit / rollback go through pgx.BeginTxFunc: fn returning nil commits,
// an error rolls back and is returned as-is, a panic rolls back and
// re-panics. Do not add a recover around fn.
func InTxWithOptions(ctx context.Context, db DB, opts *sql.TxOptions, fn func(context.Context) error) error {
	if fn == nil {
		return errNilTxFn
	}
	ctx = normalizeContext(ctx)
	if txFromContext(ctx) != nil {
		return fn(ctx)
	}
	if db == nil {
		return errNilDB
	}
	txOpts, err := toPgxTxOptions(opts)
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, txOpts, func(tx pgx.Tx) error {
		return fn(WithTx(ctx, tx))
	})
}

// TxManager binds InTx / InTxWithOptions to a DB so services can depend on
// the service.Transactor interface instead of the pool.
type TxManager struct {
	db DB
}

// NewTxManager builds a TxManager; internal/server.go wires it.
func NewTxManager(db DB) *TxManager {
	return &TxManager{db: db}
}

// InTx runs fn in a transaction; see the package-level InTx.
func (m *TxManager) InTx(ctx context.Context, fn func(context.Context) error) error {
	return InTx(ctx, m.db, fn)
}

// InTxWithOptions runs fn with custom isolation; see the package-level
// InTxWithOptions.
func (m *TxManager) InTxWithOptions(ctx context.Context, opts *sql.TxOptions, fn func(context.Context) error) error {
	return InTxWithOptions(ctx, m.db, opts, fn)
}

// toPgxTxOptions maps database/sql options to pgx. Isolation levels
// Postgres does not support are rejected instead of silently downgraded.
func toPgxTxOptions(opts *sql.TxOptions) (pgx.TxOptions, error) {
	var out pgx.TxOptions
	if opts == nil {
		return out, nil
	}
	if opts.ReadOnly {
		out.AccessMode = pgx.ReadOnly
	}
	switch opts.Isolation {
	case sql.LevelDefault:
	case sql.LevelReadUncommitted:
		out.IsoLevel = pgx.ReadUncommitted
	case sql.LevelReadCommitted:
		out.IsoLevel = pgx.ReadCommitted
	case sql.LevelRepeatableRead:
		out.IsoLevel = pgx.RepeatableRead
	case sql.LevelSerializable:
		out.IsoLevel = pgx.Serializable
	default:
		return pgx.TxOptions{}, fmt.Errorf("repository: unsupported isolation level %s", opts.Isolation)
	}
	return out, nil
}

// dbFromContext returns the transaction in ctx, or db when there is none.
// Every repository method must go through it, otherwise calls inside InTx
// would bypass the transaction.
func dbFromContext(ctx context.Context, db DB) sqlcdb.DBTX {
	if tx := txFromContext(ctx); tx != nil {
		return tx
	}
	return db
}

// txFromContext returns the active transaction handle, or nil.
func txFromContext(ctx context.Context) sqlcdb.DBTX {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value(txKey{}).(sqlcdb.DBTX)
	return tx
}

// normalizeContext turns a nil ctx into context.Background so a careless
// caller does not crash the driver.
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
```

- [ ] **Step 5: 重写 `internal/repository/example.go`**

```go
package repository

// Example repository template: the repository is the only layer that
// touches SQL.
//
//   - SQL lives in queries/example.sql; `make sqlc` generates package
//     sqlcdb. This file only picks the connection, calls the generated
//     method and maps rows to model types.
//   - Always go through dbFromContext(ctx, r.db) so calls inside InTx use
//     the transaction; pass ctx through, never context.Background().
//   - sqlcdb row types stay inside this package; map them with
//     toExampleModel.
//
// Integration test template: example_integration_test.go
// (//go:build integration), run via make test-integration.

import (
	"context"
	"database/sql"

	"go-skeleton/internal/model"
	"go-skeleton/internal/repository/sqlcdb"
)

// ExampleRepository persists examples.
type ExampleRepository struct {
	db DB
}

// NewExampleRepository builds an ExampleRepository; internal/server.go
// wires db (a *pgxpool.Pool).
func NewExampleRepository(db DB) *ExampleRepository {
	return &ExampleRepository{db: db}
}

// Create inserts an example and fills ID / timestamps from RETURNING.
func (r *ExampleRepository) Create(ctx context.Context, example *model.Example) error {
	row, err := sqlcdb.New(dbFromContext(ctx, r.db)).CreateExample(ctx, example.Name)
	if err != nil {
		return err
	}
	*example = toExampleModel(row)
	return nil
}

// List returns a page ordered by id DESC plus the total count. Both run in
// one REPEATABLE READ read-only transaction so total and rows share a
// snapshot.
func (r *ExampleRepository) List(ctx context.Context, limit, offset int) ([]model.Example, int64, error) {
	var (
		total    int64
		examples []model.Example
	)
	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	err := InTxWithOptions(ctx, r.db, opts, func(txCtx context.Context) error {
		q := sqlcdb.New(dbFromContext(txCtx, r.db))
		n, err := q.CountExamples(txCtx)
		if err != nil {
			return err
		}
		rows, err := q.ListExamples(txCtx, sqlcdb.ListExamplesParams{Lim: int64(limit), Off: int64(offset)})
		if err != nil {
			return err
		}
		total = n
		examples = make([]model.Example, 0, len(rows))
		for _, row := range rows {
			examples = append(examples, toExampleModel(row))
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return examples, total, nil
}

func toExampleModel(row sqlcdb.Example) model.Example {
	return model.Example{
		ID:        row.ID,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}
```

- [ ] **Step 6: 改 `internal/model/example.go`，删 `example_test.go`**

```go
package model

// Example model template: a plain data struct used by the application.
//
//   - JSON tags define the external contract; the repository maps sqlc rows
//     into this struct.
//   - Do not attach business rules (auth, state machines, external calls)
//     here; they belong in service.
//   - The DDL source of truth is migrations/*.sql. Keep fields in sync with
//     the migration by hand.

import "time"

// Example is the sample model wiring handler → service → repository →
// model; copy it when adding a new business model.
type Example struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

```bash
git rm -q internal/model/example_test.go
```
（`TableName` 随 GORM 一起移除，其唯一测试同步删除。）

- [ ] **Step 7: 跑 repository 测试**

Run: `go test ./internal/repository/... ./internal/model/... -v`
Expected: PASS（全部 `TestInTx*`、`TestToPgxTxOptions`、`TestTxManagerDelegates`、`TestRowMocksScan`、`TestExampleRepository*`）。

- [ ] **Step 8: 新增 `internal/service/tx.go`**

```go
package service

import (
	"context"
	"database/sql"
)

// Transactor lets a service run several repository calls in one
// transaction. The production implementation is repository.TxManager,
// wired in internal/server.go. Always pass txCtx to repositories inside
// fn, otherwise they bypass the transaction.
type Transactor interface {
	InTx(ctx context.Context, fn func(txCtx context.Context) error) error
	InTxWithOptions(ctx context.Context, opts *sql.TxOptions, fn func(txCtx context.Context) error) error
}
```

- [ ] **Step 9: 改调用方**

`internal/service/example.go:141`：`zap.Uint64("example_id", example.ID)` → `zap.Int64("example_id", example.ID)`。

`internal/server.go`：
- 第 257 行：`case reg.DB == nil || reg.DB.Pool() == nil:`
- 第 268 行：`db := reg.DB.Pool()`
- 在 `validateHTTPRegistry` 函数上方新增：
```go
// repository.TxManager is the production service.Transactor; inject it via
// repository.NewTxManager(db) when a service needs cross-repository
// transactions.
var _ service.Transactor = (*repository.TxManager)(nil)
```

`internal/worker.go`：
- 第 117 行：`repo := repository.NewExampleRepository(reg.DB.Pool())`
- 第 98 行注释 `（走 repository → gorm 落库）` 改为 `（走 repository → sqlc 落库）`；第 100 行 `worker 包本身不 import gorm` 改为 `worker 包本身不 import pgx / sqlcdb`。

- [ ] **Step 10: 重写 `internal/repository/example_integration_test.go`**

保留文件头注释（把 `验证 GORM 查询行为` 改成 `验证 sqlc 查询行为`），import 与 helper 替换为：
```go
import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-skeleton/internal/model"
	"go-skeleton/internal/repository"
)

// openTestPool reads the DSN from POSTGRES and skips when unset, so CI /
// local runs without a database do not fail.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES")
	if dsn == "" {
		t.Skip("POSTGRES env not set; skipping integration test")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
```
测试函数首行 `db := openTestDB(t)` → `pool := openTestPool(t)`，`repository.NewExampleRepository(db)` → `repository.NewExampleRepository(pool)`；其余断言不变。

Run: `go vet -tags integration ./internal/repository/...`
Expected: 无输出（编译通过）。有 docker 时再跑 `make dev-up && go run ./cmd/migrate && make test-integration`，Expected PASS。

- [ ] **Step 11: 全量校验并提交**

Run: `make verify`
Expected: `=== verify OK ===`。此时 `grep -rn "gorm" --include='*.go' internal/repository internal/model` 无输出。

```bash
git add internal/repository/tx.go internal/repository/tx_test.go internal/repository/example.go internal/repository/example_test.go internal/repository/example_integration_test.go internal/model/example.go internal/service/tx.go internal/service/example.go internal/server.go internal/worker.go
git commit -F - <<'EOF'
refactor(repository): repository 切换到 sqlc + pgx 事务

- 新增 repository.DB 接口（sqlcdb.DBTX + BeginTx），*pgxpool.Pool 直接满足
- InTx / InTxWithOptions 改用 pgx.BeginTxFunc，嵌套复用外层事务，不支持的隔离级别报错
- 新增 TxManager 与 service.Transactor 接口，server.go 做编译期断言
- ExampleRepository 改调 sqlc 生成代码，行类型经 toExampleModel 映射，对外签名不变
- model.Example 去掉 gorm tag，ID 改为 int64；删除 TableName 及其测试
- 测试改为手写 mockDBTX / mockTx / mockRows，覆盖提交、回滚、panic、嵌套与隔离级别映射
- server.go / worker.go 改用 reg.DB.Pool()；集成测试改用 pgxpool
EOF
```

---

### Task 5: 删除 GORM 依赖与架构规则更新

**Files:**
- Modify: `pkg/database/database.go`（删 gorm 字段、`DB()`；`NewManager` 不再返回 error）
- Modify: `internal/server_test.go`（适配 `NewManager`）
- Modify: `internal/worker/handler.go:18-22, 51-56`（注释）
- Modify: `scripts/architecture-verify.go:82-94` + 新增规则 5 + 新 helper
- Modify: `scripts/architecture_verify_test.go`
- Modify: `Makefile`（`architecture-verify` 描述）
- Modify: `go.mod` / `go.sum`

**Interfaces:**
- Consumes: Task 4 之后已无 `(*DBManager).DB()` 调用方
- Produces: `database.NewManager(pool *pgxpool.Pool) *DBManager`；architecture-verify 规则 2（pgx / sqlcdb 限定目录）与规则 5（全仓禁 `gorm.io/`）

- [ ] **Step 1: 写架构规则测试（先红）**

`scripts/architecture_verify_test.go`：文件头注释第 2-3 行改为：
```go
// architecture_verify_test.go 覆盖 architecture-verify.go 的分层依赖规则:
// 规则 1 service 不 import gin、规则 2 pgx / sqlcdb 限定到 repository /
// bootstrap / pkg/database、规则 5 全仓禁止 gorm。
```
用下面两个测试替换 `TestArchitectureVerify_GormOutsideAllowList`：
```go
func TestArchitectureVerify_PgxOutsideAllowList(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// Rule 2 hit: handler must not import pgx or the sqlc package.
	writeFile(t, filepath.Join(dir, "internal", "handler", "x.go"), `package handler

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"go-skeleton/internal/repository/sqlcdb"
)

var (
	_ *pgxpool.Pool
	_ sqlcdb.DBTX
)
`)
	// Allowed: repository and pkg/database.
	writeFile(t, filepath.Join(dir, "internal", "repository", "ok.go"), `package repository

import "github.com/jackc/pgx/v5"

var _ pgx.Tx
`)
	writeFile(t, filepath.Join(dir, "pkg", "database", "ok.go"), `package database

import "github.com/jackc/pgx/v5/pgxpool"

var _ *pgxpool.Pool
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when handler imports pgx\n%s", out)
	}
	if !strings.Contains(out, "rule 2") || !strings.Contains(out, "internal/handler/x.go") {
		t.Errorf("expected rule 2 + handler/x.go, got:\n%s", out)
	}
	if !strings.Contains(out, "go-skeleton/internal/repository/sqlcdb") {
		t.Errorf("expected sqlcdb import flagged, got:\n%s", out)
	}
	for _, ok := range []string{"internal/repository/ok.go", "pkg/database/ok.go"} {
		if strings.Contains(out, ok) {
			t.Errorf("%s is in allow list, should not be flagged:\n%s", ok, out)
		}
	}
}

func TestArchitectureVerify_GormForbidden(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// Rule 5: gorm is gone, even repository may not import it.
	writeFile(t, filepath.Join(dir, "internal", "repository", "x.go"), `package repository

import "gorm.io/gorm"

var _ = gorm.DB{}
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when anything imports gorm\n%s", out)
	}
	if !strings.Contains(out, "rule 5") || !strings.Contains(out, "internal/repository/x.go") {
		t.Errorf("expected rule 5 + repository/x.go, got:\n%s", out)
	}
}
```
（`TestArchitectureVerify_Clean` 里若有"4 条规则"字样的注释改为"5 条规则"。）

- [ ] **Step 2: 确认失败**

Run: `go test ./scripts/ -run TestArchitectureVerify -v`
Expected: `PgxOutsideAllowList` 与 `GormForbidden` FAIL。

- [ ] **Step 3: 改 `scripts/architecture-verify.go`**

规则 2 替换为：
```go
		// 规则 2：pgx 与 sqlc 生成包仅允许 repository / bootstrap / pkg/database。
		// 其他层通过 service 包里的接口隔离，不直接接触驱动或生成代码。
		{
			id:   2,
			desc: "github.com/jackc/pgx 与 internal/repository/sqlcdb 仅允许 internal/{repository,bootstrap} 与 pkg/database 使用",
			check: importPrefixesExcept(
				[]string{"github.com/jackc/pgx/", modulePath + "/internal/repository/sqlcdb"},
				"internal/repository",
				"internal/bootstrap",
				"pkg/database",
			),
		},
```
规则 4 之后追加：
```go
		// 规则 5：数据访问已迁移到 sqlc + pgx，全仓禁止重新引入 GORM。
		{
			id:    5,
			desc:  "禁止 import gorm.io/*（数据访问统一走 sqlc + pgx）",
			check: importPrefixInDirs("gorm.io/", "."),
		},
```
把 `importExcept` 函数整体替换为（原函数不再有调用方）：
```go
// importPrefixesExcept flags any import starting with one of prefixes in a
// file outside every allow directory.
func importPrefixesExcept(prefixes []string, allow ...string) func() ([]violation, error) {
	return func() ([]violation, error) {
		return walkImports(".", func(file string, spec *ast.ImportSpec) *violation {
			p := importPath(spec)
			if !slices.ContainsFunc(prefixes, func(prefix string) bool { return strings.HasPrefix(p, prefix) }) {
				return nil
			}
			for _, a := range allow {
				if strings.HasPrefix(file, a+string(os.PathSeparator)) {
					return nil
				}
			}
			return &violation{
				file: file,
				line: lineOf(spec.Pos()),
				note: fmt.Sprintf(`import %q`, p),
			}
		})
	}
}
```
import 块若无 `"slices"` 则补上。

- [ ] **Step 4: 删除 GORM 桥**

`pkg/database/database.go`：
- import 去掉 `gorm.io/driver/postgres`、`gorm.io/gorm`、`gorm.io/gorm/logger`；
- 删除 `DBManager.gorm` 字段及其注释、删除 `DB()` 方法；
- `NewManager` 改为：
```go
// NewManager wraps an existing pool. Tests can pass a pool pointing at an
// unreachable address because pgxpool does not connect until first use.
func NewManager(pool *pgxpool.Pool) *DBManager {
	return &DBManager{pool: pool, sqlDB: stdlib.OpenDBFromPool(pool)}
}
```
- `Init` 中 `m, err := NewManager(pool) ... return m, nil` 段改为：
```go
	applog.L().Info("postgres pool created")
	return NewManager(pool), nil
```

`internal/server_test.go`：
```go
	dbMgr := database.NewManager(pool)
	t.Cleanup(func() { _ = dbMgr.Close() })
```
（删掉 `NewManager` 的 err 分支。）

- [ ] **Step 5: 清理注释与 Makefile 描述**

`internal/worker/handler.go` 第 19-21 行改为：
```go
// 提供一个 "有名有姓" 的方法签名而不是空 interface。worker 不直接持数据库
// 连接——那是 repository 的事；业务逻辑挂在 service / usecase 上，通过
// 本地接口隔离引入。
```
第 53-55 行改为：
```go
// 故意**不**包含数据库连接：repository 是项目里唯一允许接触 pgx / sqlcdb 的层
// （见 CLAUDE.md 分层规则）。Worker handler 需要落库的话，走 service 接口
// → repository → sqlc，而不是在 worker 包内直接拿连接池。
```
`Makefile` 的 `architecture-verify:` 描述改为：
```make
architecture-verify: ## 校验分层 import 边界（gin 外溢、pgx / sqlcdb 外溢、禁 gorm、pkg→internal 反向依赖、service/handler 误用 context.Background）
```

- [ ] **Step 6: 整理依赖并确认 GORM 清零**

Run: `go mod tidy && grep -n "gorm" go.mod; grep -rln "gorm" --include='*.go' . | grep -v '^./scripts/'`
Expected: 两条 grep 都无输出（scripts 里的 drop-example 文案在 Task 6 处理）。

- [ ] **Step 7: 全量校验并提交**

Run: `go test ./scripts/ -run TestArchitectureVerify -v && make verify`
Expected: PASS；`=== verify OK ===`。

```bash
git add pkg/database/database.go internal/server_test.go internal/worker/handler.go scripts/architecture-verify.go scripts/architecture_verify_test.go Makefile go.mod go.sum
git commit -F - <<'EOF'
refactor(database): 移除 GORM 依赖并收紧架构规则

- DBManager 删除 GORM 过渡桥与 DB()，NewManager 不再返回 error
- architecture-verify 规则 2 改为 pgx / sqlcdb 仅限 repository、bootstrap、pkg/database
- 新增规则 5：全仓禁止 import gorm.io/*
- 规则测试覆盖 pgx 越界、sqlcdb 越界、允许目录与 gorm 禁用
- go mod tidy 移除 gorm.io/gorm 与 gorm.io/driver/postgres
EOF
```

---

### Task 6: 脚手架适配 sqlc（new-endpoint / drop-example）

**Files:**
- Modify: `scripts/new-endpoint.go:1378-1427`（`renderRepository` / `renderModel`）、`1499-1521`（`renderRepositoryTest`）
- Modify: `scripts/drop-example.go`（`main` 步骤、`filesToDelete`、新增 `ensureQueriesPlaceholder` / `regenerateSQLC`、`patchServerGo`、`patchWorkerGo`、`rewriteWorkerHandler` 文案）

**Interfaces:**
- Consumes: `repository.DB`、`mockDBTX`（Task 4）、`make sqlc`（Task 1）
- Produces: 新生成的 repository 骨架 `type XxxRepository struct{ db DB }` + `NewXxxRepository(db DB)`；`server.go` 注入行仍是 `repository.NewXxxRepository(db)`（`db := reg.DB.Pool()`），`patchServer` 无需改动。

- [ ] **Step 1: 改 `renderRepository`**

```go
// renderRepository 生成 internal/repository/<lower>.go。
// 只给 struct + New：OpenAPI 不知道表结构，查询由开发者在
// queries/<lower>.sql 里手写后 make sqlc 生成，再在这里调用。
func renderRepository(name, lower string, _ []operation) string {
	return fmt.Sprintf(`package repository

// %[1]sRepository 由 make new-endpoint NAME=%[1]s 生成的骨架。
// 唯一允许写 SQL 的层：SQL 写在 queries/*.sql，make sqlc 生成 sqlcdb 包。
//
// 加查询方法的步骤：
//  1. 在 internal/repository/queries/%[2]s.sql 写 "-- name: Xxx :one" 查询，跑 make sqlc
//  2. 在 internal/service/%[2]s.go 的 %[1]sRepository 接口里加方法签名
//  3. 在这里实现：sqlcdb.New(dbFromContext(ctx, r.db)).Xxx(ctx, ...)，把行映射成 model
//  4. 跨 repository 事务由 service 用 Transactor.InTx 包，这里不自己开事务

type %[1]sRepository struct {
	db DB
}

// New%[1]sRepository 构造 %[1]sRepository。db 由 internal/server.go 装配（*pgxpool.Pool）。
func New%[1]sRepository(db DB) *%[1]sRepository {
	return &%[1]sRepository{db: db}
}
`, name, lower)
}
```

- [ ] **Step 2: 改 `renderModel`**

```go
// renderModel 生成 internal/model/<lower>.go——普通数据 struct 骨架。
func renderModel(name, lower string) string {
	return fmt.Sprintf(`package model

// %[1]s 是 %[2]s 资源的数据结构。由 make new-endpoint NAME=%[1]s 生成。
// 表结构真相源是 migrations/*.sql；repository 把 sqlc 行类型映射成本 struct，
// 字段与迁移文件保持一致由开发者维护。

import "time"

// %[1]s TODO: 按业务字段补 columns。
type %[1]s struct {
	ID        int64     `+"`json:\"id\"`"+`
	CreatedAt time.Time `+"`json:\"created_at\"`"+`
	UpdatedAt time.Time `+"`json:\"updated_at\"`"+`
}
`, name, lower)
}
```

- [ ] **Step 3: 改 `renderRepositoryTest` 注释**（顺手修 `new-endpoke` 笔误）

把模板开头两行注释替换为：
```go
// %[1]sRepository smoke 测试：由 make new-endpoint NAME=%[1]s 生成。
// 真实查询出现后按 example_test.go 风格，用 tx_test.go 里的 mockDBTX /
// mockTx 断言 SQL 与参数，不连真实 DB。
```

- [ ] **Step 4: 脚手架黑盒回归**

Run: `make scaffold-verify`
Expected: PASS（fixture 只断言注入行前缀 `orderRepository := repository.NewOrderRepository`，未受影响）。若有断言引用旧模板文本（`gorm` / `uint`），同步改测试期望。

- [ ] **Step 5: drop-example——删除列表与 main 步骤**

`filesToDelete()`：
- 删掉 `"internal/model/example_test.go",` 一行（Task 4 已删该文件）；
- 追加：
```go
		"internal/worker_test.go",
		"internal/repository/queries/example.sql",
```

`main()` 中 `must(ensureMigrationsPlaceholder(), ...)` 之后加：
```go
	must(ensureQueriesPlaceholder(), "leave sqlc queries placeholder")
```
`must(runMake("oapi"), "make oapi")` 之后加：
```go
	must(regenerateSQLC(), "regenerate sqlc output")
```

- [ ] **Step 6: drop-example——新增两个函数**（放在 `ensureMigrationsPlaceholder` 之后）

```go
// ensureQueriesPlaceholder 给 internal/repository/queries/ 留一条占位查询。
//
// 缘由：sqlc 在 queries 目录找不到任何查询会直接报错，make sqlc /
// sqlc-verify 随之失败。占位查询只 SELECT 1，不依赖任何表。
func ensureQueriesPlaceholder() error {
	const dir = "internal/repository/queries"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			return nil // 已有查询（重跑 / 用户手写过），不再插占位。
		}
	}
	const path = dir + "/placeholder.sql"
	const body = `-- 这是 drop-example 留下的占位查询：sqlc 在 queries/ 下找不到任何查询会报错，
-- 导致 make sqlc / sqlc-verify 失败。
-- 接入第一条真业务查询后删掉本文件并重跑 make sqlc。

-- name: Placeholder :exec
SELECT 1;
`
	if err := writeFile(path, body); err != nil {
		return err
	}
	log.Printf("  ✓ left sqlc queries placeholder %s (接入真业务后删它)", path)
	return nil
}

// regenerateSQLC 清空 sqlc 输出目录后重新生成：sqlc 不会删除已失效的
// 生成文件（如 example.sql.go），不清空会留下引用已删表的代码。
func regenerateSQLC() error {
	const out = "internal/repository/sqlcdb"
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	log.Printf("  ✓ cleared %s", out)
	return runMake("sqlc")
}
```

- [ ] **Step 7: drop-example——`patchServerGo` 适配**

`silence unused db var after Example removal` 这条 patch 的 old / new 字符串里的 `"\tdb := reg.DB.DB()\n"` 都改为 `"\tdb := reg.DB.Pool()\n"`，上方注释 `` `db := reg.DB.DB()` `` 同步改为 `` `db := reg.DB.Pool()` ``。

- [ ] **Step 8: drop-example——修复 `patchWorkerGo`（既有缺陷）**

master 上该函数的 `oldBlock` 与当前 `buildWorkerDeps` 已漂移，被静默跳过，导致 drop 后 `internal/worker.go` 仍 import 已删除的 example service。把整个 `patchWorkerGo` 替换为按函数头正则定位的实现：

```go
func patchWorkerGo() error {
	const path = "internal/worker.go"
	content, err := readFile(path)
	if err != nil {
		return err
	}
	// Match from the doc comment to the closing brace of buildWorkerDeps so
	// comment edits upstream do not make the patch silently skip.
	re := regexp.MustCompile(`(?s)// buildWorkerDeps 把 Registry 翻译成 worker handler 用的 Deps。\n.*?\nfunc buildWorkerDeps\(reg \*bootstrap\.Registry\) \(\*worker\.Deps, error\) \{\n.*?\n\}\n`)
	if !re.MatchString(content) {
		return fmt.Errorf("%s: buildWorkerDeps block not found; update drop-example to match", path)
	}
	const newBlock = `// buildWorkerDeps 把 Registry 翻译成 worker handler 用的 Deps。
// 真实业务自行在这里把对应 service 注入 Deps 的具体字段。返回 error 让
// caller 在依赖装配失败时 fail-fast——production 漏注入业务 processor
// 比 panic 更危险（消息会被 noop ack 掉）。
func buildWorkerDeps(reg *bootstrap.Registry) (*worker.Deps, error) {
	return &worker.Deps{
		Cache: reg.Cache,
		Queue: reg.Queue,
	}, nil
}
`
	content = re.ReplaceAllLiteralString(content, newBlock)
	content = strings.Replace(content, "\t\"go-skeleton/internal/repository\"\n\t\"go-skeleton/internal/service\"\n", "", 1)
	if err := writeFile(path, content); err != nil {
		return err
	}
	log.Printf("  ✓ patched %s (drop buildWorkerDeps Example wiring + imports)", path)
	return nil
}
```
注意：重跑时（已 drop）正则不匹配会返回 error——这与脚本"拒绝 dirty checkout"的前提一致（第二次运行在第一步就会被拦下），可接受。

- [ ] **Step 9: drop-example——`rewriteWorkerHandler` 文案去 gorm**

把 body 里 `Deps` 注释三行替换为与 Task 5 Step 5 相同的新文案：
```go
// 故意**不**包含数据库连接：repository 是项目里唯一允许接触 pgx / sqlcdb 的层
// （见 CLAUDE.md 分层规则）。Worker handler 需要落库的话，走 service 接口
// → repository → sqlc，而不是在 worker 包内直接拿连接池。
```

- [ ] **Step 10: 在一次性 worktree 里实跑 drop-example**

```bash
git add scripts/new-endpoint.go scripts/drop-example.go
git commit -F - <<'EOF'
refactor(build): 脚手架适配 sqlc 并修复 drop-example

- new-endpoint 的 repository 骨架改为 DB 接口 + sqlc 调用指引，model 骨架改为 int64 ID + json tag
- repository 测试模板指向 mockDBTX / mockTx，修正 new-endpoke 笔误
- drop-example 删除 example 查询并留占位查询，清空 sqlcdb 后重新生成
- 修复 drop-example 既有缺陷：buildWorkerDeps 块改为正则定位并清理 import，删除 internal/worker_test.go
- 同步 reg.DB.Pool() 与去 gorm 文案
EOF
git worktree add --detach /tmp/sqlc-drop-check HEAD
cd /tmp/sqlc-drop-check && make drop-example
```
Expected: 脚本末尾的子集校验（fmt / vet / test / lint / architecture / env / tidy / docs）全绿，日志含 `left sqlc queries placeholder` 与 `cleared internal/repository/sqlcdb`。

接着在该 worktree 里提交（detached HEAD，不产生分支）并跑完整 verify：
```bash
cd /tmp/sqlc-drop-check && git add -A && git commit -qm "tmp: drop-example check" && make verify
```
Expected: `=== verify OK ===`（含 sqlc-verify / oapi-verify）。失败则回到主工作区修 `drop-example.go`，`git commit --amend`，删除该 worktree 后重建再试。

- [ ] **Step 11: 在一次性 worktree 里实跑 new-endpoint**

```bash
cd /Users/cooper/Code/go-develop/go-example
git worktree add --detach /tmp/sqlc-demo-check HEAD
cd /tmp/sqlc-demo-check && python3 - <<'EOF'
p = "api/openapi.yaml"
s = open(p).read()
snippet = """paths:
  /api/v1/demos:
    x-resource: Demo
    get:
      tags: [example]
      summary: 演示列表
      description: 脚手架冒烟用的临时 endpoint。
      operationId: listDemos
      responses:
        '200':
          description: 成功
"""
assert s.count("\npaths:\n") == 1
open(p, "w").write(s.replace("\npaths:\n", "\n" + snippet, 1))
EOF
make oapi && make new-endpoint NAME=Demo && git add -A && git commit -qm "tmp: new-endpoint check" && make verify
```
Expected: 生成 `internal/repository/demo.go`（含 `db DB`）等五层骨架，`=== verify OK ===`。

- [ ] **Step 12: 清理一次性 worktree**

两个 worktree 都是本 Task 在 `/tmp` 下新建的 detached 检出（无分支），确认 Step 10 / 11 通过后移除：
```bash
cd /Users/cooper/Code/go-develop/go-example
git worktree remove --force /tmp/sqlc-drop-check
git worktree remove --force /tmp/sqlc-demo-check
git worktree list
```
Expected: 只剩主工作区。

- [ ] **Step 13: PR2 收尾**

Run: `make verify GO_TEST_FLAGS=-race && make scaffold-verify && make sec`
Expected: 全绿。本地有 docker 时再做一次端到端：`make dev-up && go run ./cmd/migrate && make test-integration`，并启动 API 调 `POST /api/v1/examples` + `GET /api/v1/examples` 确认创建与列表可用。然后**停下来询问用户**是否 push 并开 PR2。

---

# PR3：文档（分支 `docs/sqlc-docs`，PR2 合并后从最新 master 拉出）

### Task 7: 更新 AI 规则文件、开发文档与 CHANGELOG

**Files:**
- Modify: `CLAUDE.md`、`AGENTS.md`（同样的改动；AGENTS.md 行号比 CLAUDE.md 大约 +9）
- Modify: `docs/runbook.md`、`docs/development.md`、`CHANGELOG.md`

**Interfaces:**
- Consumes: PR1 / PR2 的最终命名（`repository.DB`、`TxManager`、`service.Transactor`、`make sqlc` / `sqlc-verify`、规则 5、`DB_*` 配置项）

- [ ] **Step 1: CLAUDE.md 与 AGENTS.md 同步修改**（每一处在两份文件里做同样替换）

1. 技术栈行：`Go 1.27+ + Gin + GORM + PostgreSQL + Redis + Asynq。` → `Go 1.27+ + Gin + sqlc（pgx/v5）+ PostgreSQL + Redis + Asynq。`
2. 顶层目录表 `internal/handler/ service/ repository/ model/` 行之后新增一行：
```markdown
| `internal/repository/queries/` `internal/repository/sqlcdb/` | SQL 查询 / sqlc 生成代码 | 查询只写在 `queries/*.sql`；`sqlcdb/` 由 `make sqlc` 生成、入库、**禁止手改** |
```
3. 迁移段里 `不用 GORM AutoMigrate，` → `sqlc 也直接以 migrations/ 为 schema 输入，`。
4. 迁移段之后新增一段：
```markdown
查询用 [sqlc](https://sqlc.dev)（`sqlc.yaml`，pgx/v5）：在 `internal/repository/queries/<资源>.sql` 写 `-- name: Xxx :one|:many|:exec` 查询 → `make sqlc` 生成 `internal/repository/sqlcdb` → repository 调生成方法并把行类型映射成 `model`。`make verify` 里的 `sqlc-verify` 会重新生成并 `git diff` 校验产物已提交。可选条件优先用 `sqlc.narg()`；确实写不出的动态查询允许在 repository 内用 pgx 手写参数化 SQL，**不引入** query builder。`LIMIT` / `OFFSET` 参数写 `sqlc.arg(lim)::bigint` 让生成类型为 int64。
```
5. service 规则 `不能直接写 GORM 链式调用。` → `不能直接写 SQL、不能 import pgx / sqlcdb。`
6. 事务模板代码块替换为：
```go
// service 层：跨 OrderRepository + InventoryRepository 的下单流程
// s.tx 是 service.Transactor，由 internal/server.go 注入 repository.NewTxManager(db)
func (s *OrderService) Place(ctx context.Context, req *PlaceOrderReq) (*Order, error) {
    var created *Order
    err := s.tx.InTx(ctx, func(txCtx context.Context) error {
        // 1) 扣减库存（库存不足 repo 返业务错，整事务回滚）
        if err := s.inventory.Reserve(txCtx, req.SkuID, req.Qty); err != nil {
            return err  // 透传 errcode.Error，InTx 不会包装
        }
        // 2) 落订单
        order, err := s.orders.Create(txCtx, req.ToModel())
        if err != nil {
            return err
        }
        created = order
        return nil
    })
    if err != nil {
        return nil, err  // 已是 errcode.Error；handler 走 WriteError 自动映射 HTTP
    }
    return created, nil
}
```
   模板上方一句 `**只能** 在 service 层用 repository.InTx 包起来` → `**只能** 在 service 层用注入的 service.Transactor（生产实现 repository.TxManager）包起来`。
7. 要点列表：`fn 返 error → GORM rollback；返 nil → commit。中途 panic 也会 rollback（GORM 默认行为，不要写自己的 recover 绕过）。` → `fn 返 error → rollback；返 nil → commit。中途 panic 也会 rollback 后继续上抛（pgx.BeginTxFunc 行为，不要写自己的 recover 绕过）。`；`repository.InTxWithOptions(ctx, db, &sql.TxOptions{...}, fn)` → `s.tx.InTxWithOptions(ctx, &sql.TxOptions{...}, fn)`，并在该条末尾补：`Postgres 不支持的隔离级别（如 LevelSnapshot）直接返 error。`
8. repository 小节三条替换为：
```markdown
- **唯一允许写 SQL、import pgx / sqlcdb 的层**（`make architecture-verify` 规则 2 拦截越界，规则 5 全仓禁止 gorm）。
- 查询写 `queries/*.sql` 走 sqlc 生成，调用统一 `sqlcdb.New(dbFromContext(ctx, r.db)).Xxx(ctx, ...)`；ctx 一路透传，禁止 `context.Background()` 替换。sqlcdb 行类型不出 repository，映射成 `model` 再返回。
- repository 依赖 `repository.DB` 接口（`*pgxpool.Pool` 满足）。单 repository 内的读一致性可以自己用 `InTxWithOptions`（如分页 total 走 `sql.LevelRepeatableRead` + `ReadOnly`）；跨 repository 事务由 service 通过 `Transactor` 决定。嵌套调用 opts 会被忽略，isolation 必须在最外层定。
```
9. model 小节：`纯 GORM 数据结构，不挂带业务规则的方法。` → `普通数据 struct（只有 json tag），不挂带业务规则的方法。`
10. 常犯错误：`❌ repository 之外的层 import gorm.io/gorm` → `❌ repository 之外的层 import pgx / sqlcdb 或手写 SQL`。
11. 测试工具栈：`gorm.io/gorm 的 DryRun` → `repository 包 tx_test.go 里的手写 mockDBTX / mockTx / mockRows`。
12. 各层测试表 repository 行：
```markdown
| `repository` | 手写 `mockDBTX`（Exec / Query / QueryRow func 字段）+ `mockTx`（内嵌 `pgx.Tx`，只覆盖用到的方法）断言 SQL 片段与参数，**不连真实 DB** | `internal/repository/example_test.go`、`internal/repository/tx_test.go` |
```
13. 目录树：`repository/ 数据访问层（唯一允许写 GORM）` → `repository/ 数据访问层（唯一允许写 SQL）`，其下补 `queries/` 与 `sqlcdb/` 两行；`model/ GORM 数据结构` → `model/ 数据 struct`；`database/ GORM 初始化 + 健康检查 + zap SQL 日志` → `database/ pgxpool 初始化 + 健康检查 + pgx tracer SQL 日志`；根目录补一行 `├── sqlc.yaml  sqlc 配置（schema=migrations，queries=internal/repository/queries）`。
14. 所有 `make verify   # fmt + vet + ... + oapi-verify + ...` 说明里，在 `oapi-verify` 后补 `+ sqlc-verify`。
15. `AI 助手提示` 列表追加一条：
```markdown
- **不要修改 `internal/repository/sqlcdb/`**。改 `internal/repository/queries/*.sql` 或 `migrations/*.sql` 后跑 `make sqlc`；`modernize` 对该目录报的 `interface{}` 属生成代码，忽略。
```

- [ ] **Step 2: 校验两份规则文件同步**

Run: `make docs-verify && grep -n -i "gorm" CLAUDE.md AGENTS.md`
Expected: docs-verify 通过；grep 只剩"规则 5 全仓禁止 gorm"这类说明性提及。

- [ ] **Step 3: `docs/development.md`**

- 第 73 行 → `- service / handler 直接 import pgx / sqlcdb 或手写 SQL → 通过 service 包里的 repository 接口隔离`
- 第 138 行 → `3. 改 [internal/repository/queries/](../internal/repository/queries/) 的 SQL 与 [internal/model/](../internal/model/) 的 struct，跑 make sqlc 重新生成（struct 与迁移文件需手动保持一致）`
- 第 162 行 → `| repository | 手写 mockDBTX / mockTx 断言 SQL 与参数 | [internal/repository/example_test.go](../internal/repository/example_test.go) |`

- [ ] **Step 4: `docs/runbook.md` 新增"改 SQL / 新增查询"清单**（放在"新增 endpoint"小节之后）

```markdown
### 改 SQL / 新增查询

1. 表结构变更：`make migrate-create name=xxx` → 填 SQL（sqlc 也读这些文件作 schema）。
2. 在 `internal/repository/queries/<资源>.sql` 写查询：`-- name: GetOrder :one` + SQL；可选条件用 `sqlc.narg(name)`，分页参数写 `sqlc.arg(lim)::bigint`。
3. `make sqlc` 生成 `internal/repository/sqlcdb/`（不要手改）。
4. repository 里调 `sqlcdb.New(dbFromContext(ctx, r.db)).GetOrder(ctx, ...)`，把行映射成 `model`。
5. 用 `tx_test.go` 里的 `mockDBTX` / `mockTx` 写单测断言 SQL 与参数。
6. `make verify`（`sqlc-verify` 会确认生成产物已提交）。

排错：`sqlc generate` 报 `no queries contained in paths` → queries 目录为空，至少保留一条查询（drop-example 会留 `placeholder.sql`）。
```

- [ ] **Step 5: CHANGELOG**

在 `## [Unreleased]` → `### Changed` 下最前面加：
```markdown
- **数据访问从 GORM 迁移到 sqlc（pgx/v5）**: 查询写在
  `internal/repository/queries/*.sql`，`make sqlc` 生成
  `internal/repository/sqlcdb`（`make verify` 新增 `sqlc-verify` 校验产物已提交）。
  `pkg/database` 改为 pgxpool + pgx QueryTracer（只记 SQL 模板）；repository
  事务改用 `pgx.BeginTxFunc`，新增 `repository.TxManager` / `service.Transactor`；
  `model.Example.ID` 改为 `int64`。配置项改名：`GORM_LOG_LEVEL` → `DB_LOG_LEVEL`、
  `DB_MAX_OPEN_CONNS` → `DB_MAX_CONNS`、`DB_MAX_IDLE_CONNS` → `DB_MIN_CONNS`。
  architecture-verify 规则 2 改为限制 pgx / sqlcdb，新增规则 5 全仓禁止 gorm。
```

- [ ] **Step 6: 全量校验并提交**

Run: `make verify`
Expected: `=== verify OK ===`。

```bash
git add CLAUDE.md AGENTS.md docs/runbook.md docs/development.md CHANGELOG.md
git commit -F - <<'EOF'
docs(docs): 文档同步 sqlc 迁移

- CLAUDE.md / AGENTS.md：技术栈、sqlc 工作流、Transactor 事务模板、repository 与测试约定、目录树
- runbook 新增"改 SQL / 新增查询"清单；development.md 替换 GORM 相关说明
- CHANGELOG 记录迁移、配置项改名与架构规则变化
EOF
```
随后**停下来询问用户**是否 push 并开 PR3。
