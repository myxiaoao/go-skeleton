# GORM → sqlc（pgx/v5）迁移设计

- 日期：2026-09-28
- 状态：设计已逐段确认，待 spec 审阅
- 前提：本项目是初始化骨架，**不考虑向后兼容**（配置项直接改名、不做旧名拦截、不写迁移指南）

## 1. 目标与范围

把数据访问层从 GORM 换成 sqlc 生成代码 + pgx/v5，SQL 成为唯一真相（schema 用 `migrations/`，查询用 `internal/repository/queries/`），编译期校验 SQL 与 Go 类型的一致性。

不做：引入 query builder；`sqlc vet`（需要连库）；ErrNotFound 等尚无调用方的错误类型（首次需要时再加）。

### 目标结构

```text
sqlc.yaml                          # engine: postgresql, sql_package: pgx/v5
migrations/*.sql                   # sqlc schema 输入（不变）
internal/repository/queries/*.sql  # 手写查询（-- name: Xxx :one/:many/:exec）
internal/repository/sqlcdb/        # sqlc 生成代码（DO NOT EDIT，入库）
internal/repository/tx.go          # DB 接口 + InTx / InTxWithOptions + TxManager
internal/model/                    # 保留，普通 struct（去掉 gorm tag），ID 为 int64
pkg/database/                      # pgxpool + tracer.go（替代 gorm_logger.go）
```

依赖方向：handler → service → repository → sqlcdb / pgx。sqlcdb 生成的行类型不出 repository，repository 用纯函数 `toModel` 映射成 `model.Xxx` 后返回。

### 动态查询

优先用 `sqlc.narg()` + `COALESCE` / `IS NULL` 写法表达可选条件；确实写不出来的，允许在 repository 内用 pgx 手写 SQL（参数化，禁止拼接用户输入），不引入 squirrel 等 builder。

## 2. 交付节奏（3 个 PR）

| PR | 内容 |
| --- | --- |
| PR1 基础设施 | `sqlc.yaml` + Makefile `sqlc` / `sqlc-verify`（并入 verify，固定 v1.31.1）；`pkg/database` 改 pgxpool，GORM 通过 `stdlib.OpenDBFromPool` 共享同一连接池（过渡）；`tracer.go` 替代 GORM logger；配置项改名；`cmd/migrate` 从 pool 取 `*sql.DB` |
| PR2 切换 | example 走 sqlc；`TxManager`；ID 改 int64；测试改手写 DBTX mock；脚手架（new-endpoint / new-endpoint-check / drop-example）改 sqlc 版；architecture-verify 规则 2 更新；删除 GORM 依赖 |
| PR3 文档 | CLAUDE.md、AGENTS.md（同步）、docs/runbook.md、docs/development.md、README / README_en；CHANGELOG 记一条常规变更 |

每个 PR 独立通过 `make verify`。

## 3. repository 层与事务

### 接口

```go
// DB is satisfied by *pgxpool.Pool.
type DB interface {
    sqlcdb.DBTX
    BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func InTx(ctx context.Context, db DB, fn func(txCtx context.Context) error) error
func InTxWithOptions(ctx context.Context, db DB, opts *sql.TxOptions, fn func(txCtx context.Context) error) error
func dbFromContext(ctx context.Context, db DB) sqlcdb.DBTX

type TxManager struct{ db DB }
func NewTxManager(db DB) *TxManager
func (m *TxManager) InTx(ctx context.Context, fn func(txCtx context.Context) error) error
func (m *TxManager) InTxWithOptions(ctx context.Context, opts *sql.TxOptions, fn func(txCtx context.Context) error) error
```

语义：

- ctx 中存放当前 tx（`sqlcdb.DBTX`）；`dbFromContext` 有 tx 用 tx，否则用 base db。
- 嵌套调用直接复用外层 tx，opts 被忽略（isolation 必须在最外层定）。
- 提交 / 回滚用 `pgx.BeginTxFunc`：fn 返 nil → commit；返 error → rollback 并原样返回；panic → rollback 后继续上抛。保留"不要自己写 recover"规则。
- `*sql.TxOptions` → `pgx.TxOptions` 映射：`LevelDefault` / `ReadUncommitted` / `ReadCommitted` / `RepeatableRead` / `Serializable` 对应 pgx 同名级别；`ReadOnly` → `AccessMode: pgx.ReadOnly`；其他级别返回 error。
- service 包定义 `Transactor` 接口（`InTx` / `InTxWithOptions`），由 `internal/server.go` 注入 `TxManager`。本次 `ExampleService` 不需要跨 repository 事务，**不注入**。CLAUDE.md 事务模板改为 `s.tx.InTx(ctx, func(txCtx context.Context) error { ... })`。

### repository 写法

```go
func (r *ExampleRepository) Create(ctx context.Context, e *model.Example) error {
    row, err := sqlcdb.New(dbFromContext(ctx, r.db)).CreateExample(ctx, e.Name)
    if err != nil {
        return err
    }
    *e = toModel(row)
    return nil
}
```

- 对外方法签名不变（`Create(ctx, *model.Example) error`、`List(ctx, limit, offset) ([]model.Example, int64, error)`）。
- `List` 仍在 `InTxWithOptions(RepeatableRead + ReadOnly)` 中执行 Count 与分页查询。
- 分页查询写 `LIMIT sqlc.arg(lim)::bigint OFFSET sqlc.arg(off)::bigint`，使生成参数为 int64。
- 错误：repository 原样返回 pgx 错误；service 负责记日志并映射为 `errcode.DatabaseError`（现有行为不变）。
- `internal/service/example.go` 中 `zap.Uint64("example_id", ...)` 改为 `zap.Int64`。OpenAPI 契约里 id 本就是 int64，无需改 yaml。

## 4. pkg/database 与配置

### DBManager

| 成员 | 说明 |
| --- | --- |
| `pool *pgxpool.Pool` | 主连接池 |
| `sqlDB *sql.DB` | `stdlib.OpenDBFromPool(pool)`，给 goose（及 PR1 的 GORM）用 |
| `gorm *gorm.DB` | **仅 PR1**：`gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Discard})`，PR2 删除 |
| `NewManager(pool)` | 导出构造器，`Init` 内部复用；测试可直接传入未连接的 pool |
| `Init(ctx, cfg)` | DSN 为空返回空 manager；`pgxpool.NewWithConfig` 不连库、不 Ping——启动期 fail-fast 由已有探活保证（API / Worker 走 `probeDependencies`，migrate 自带 Ping），不重复 |
| `Pool()` / `SQLDB()` / `DB()`（仅 PR1） | 访问器 |
| `Ping(ctx)` | `pool.Ping` |
| `Close()` | 先关 `sqlDB`，再关 `pool`（`OpenDBFromPool` 关闭时不会关 pool） |

`internal/server.go` / `internal/worker.go` 改为 `reg.DB.Pool()` 传给 repository；判空条件同步改为 `reg.DB == nil || reg.DB.Pool() == nil`。`cmd/migrate` 改用 `SQLDB()`。

### 配置项（直接改名，无兼容层）

| 新名 | 默认 | 校验 | 替代 |
| --- | --- | --- | --- |
| `DB_LOG_LEVEL` | `warn` | `silent` / `error` / `warn` / `info`，非法值报错 | `GORM_LOG_LEVEL` |
| `DB_MAX_CONNS` | 30 | 1..MaxInt32 | `DB_MAX_OPEN_CONNS` |
| `DB_MIN_CONNS` | 0 | 0..`DB_MAX_CONNS` | `DB_MAX_IDLE_CONNS` |
| `DB_CONN_MAX_LIFETIME` | 30m | 不变 | — |
| `DB_CONN_MAX_IDLE_TIME` | 5m | 不变 | — |

同步更新 `config/config.go`、`config/validate.go`、对应测试、`.env.example`、README / README_en 配置表。

### tracer.go

实现 `pgx.QueryTracer`，挂在 `ConnConfig.Tracer`：

- `TraceQueryStart`：把开始时间与 SQL 模板存入 ctx。
- `TraceQueryEnd`：通过 `applog.FromContext(ctx)` 输出（带 trace_id）：
  - 出错 → error（`context.Canceled` 降级为 warn）；
  - 耗时 > 200ms → warn（带 threshold）；
  - level=info → 记录全部。
- 字段：`sql`（模板，**不含参数值**，避免泄露敏感数据）、`rows`、`elapsed`。
- 注：`pgx.ErrNoRows` 在 Scan 时才产生，tracer 看不到，无需特殊处理。

## 5. 测试策略

标准库 `testing` + 手写 mock，不引入新测试依赖。

- **repository**：`mockDBTX`（`execFunc` / `queryFunc` / `queryRowFunc` 字段）+ `mockRow`（Scan 回填）+ `mockRows`（实现 `pgx.Rows`），断言 SQL 片段（`ORDER BY id DESC`、`LIMIT`、`OFFSET`）与 int64 参数。事务场景用内嵌 `pgx.Tx` 接口的 `mockTx`，仅覆盖 `Exec` / `Query` / `QueryRow` / `Commit` / `Rollback`，未覆盖方法被调用即 nil panic，暴露意外调用。
- **tx_test.go**：commit / rollback / panic 回滚后上抛；嵌套复用且忽略 opts；`txCtx` 取到 tx；isolation 映射表驱动；不支持的级别返回 error。
- **tracer_test.go**：`zaptest/observer` 表驱动，覆盖 error / slow / info / Canceled 降级 / silent。
- **server_test.go**：用 `pgxpool.New`（创建不连库）+ `database.NewManager` 构造 registry，替代 GORM DryRun。
- **example_integration_test.go**：改用 pgxpool，保留原 build tag 与跳过条件。
- model / service / handler 测试：仅 ID 类型改为 int64。

## 6. 工具链

- `sqlc.yaml`：`sql_package: pgx/v5`；overrides 把 `pg_catalog.timestamp` / `timestamptz` 映射到 `time.Time`（pgx/v5 默认是 `pgtype.Timestamp[tz]`）。
- Makefile：
  - `_ensure-sqlc`：与其他工具一致，`go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1`；
  - `sqlc`：`sqlc generate`；
  - `sqlc-verify`：生成后 `git diff --quiet -- internal/repository/sqlcdb`，并检查该目录无未跟踪文件；并入 `verify`。
- lint：`.golangci.yml` 的 formatters / linters 两处 exclusions.paths 都加 `internal/repository/sqlcdb`（与 `internal/oapi` 同待遇）——`make fmt`（gofumpt + gci）不能改写生成代码，否则 `sqlc-verify` 必然 diff。

## 7. 脚手架

- **new-endpoint**：
  - repository 模板改为 `db DB` 字段 + `NewXxxRepository(db DB)`，方法仍返 `errcode.NotImplementedYet`，注释提示"在 `internal/repository/queries/` 写 SQL 后跑 `make sqlc`"；
  - `server.go` 注入实参改为 `reg.DB.Pool()`；
  - repository 测试模板改为 mockDBTX 版冒烟测试；
  - **不生成 queries 文件**（此时还没有表，生成的 SQL 无法通过 sqlc 编译）。
- **new-endpoint-check**：当前不依赖 GORM，只随模板字符串同步调整。
- **drop-example**：删除列表增加 `internal/repository/queries/example.sql` 与 sqlcdb 中 example 对应生成文件；仿照迁移占位，留下 `internal/repository/queries/placeholder.sql`（`-- name: Placeholder :exec` + `SELECT 1;`）后重跑 `sqlc generate`，避免空查询目录导致 sqlc 报错、`sqlc-verify` 失败；相关注释去掉 gorm 字样。
- **drop-example 既有缺陷一并修复**（master 上实测 `make drop-example` 后 `go vet` 失败）：`patchWorkerGo` 的 oldBlock 与当前 `buildWorkerDeps` 已漂移、被静默跳过，改为按函数头正则整体替换并删掉 `repository` / `service` import；`internal/worker_test.go` 全是 Example 用例，加入删除列表。
- **scaffold-verify**：fixture 随新模板更新。

## 8. architecture-verify 规则 2

- `gorm.io/*`：全仓禁止（PR2 同时从 go.mod 移除）。
- `github.com/jackc/pgx/v5/...` 与 `go-skeleton/internal/repository/sqlcdb`：仅允许 `internal/repository/**`、`internal/bootstrap`、`pkg/database`。
- `_test.go` 维持现有跳过逻辑。
- 规则补正反两面 fixture 测试。

## 9. 验收标准

- `make verify`（含新增 `sqlc-verify`）与 `make verify GO_TEST_FLAGS=-race` 全绿；`make scaffold-verify`、`make sec` 通过。
- PR2 合入后 `go.mod` 不含 `gorm.io/*`；Go 代码与脚手架模板中不再出现 gorm（CHANGELOG 历史条目与本 spec 除外）。
- `make new-endpoint NAME=Demo` 生成的仓库可直接 `make verify` 通过；`make drop-example` 后 `make verify` 通过。
- 本地 docker-compose 下 `make run-migrate` + API 创建 / 列表 example 端到端可用。
