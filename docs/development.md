# 开发工作流

按时间线串起从"克隆仓库"到"PR merge"的全过程。本文档只负责**叙事顺序**和**链接到详细文档**——具体规则 / 命令清单 / 排障指南都在原处。

> - 命令查表（横向：按场景） → [`docs/runbook.md`](./runbook.md)
> - 项目宪法（约束 + 分层规则） → [`CLAUDE.md`](../CLAUDE.md) / [`AGENTS.md`](../AGENTS.md)
> - 二进制部署 → [`docs/deploy.md`](./deploy.md)

---

## 一、第一次 onboarding（5 分钟）

新仓库克隆下来后跑一次：

```sh
make init                # 装 / 校验 pin 版本工具链（golangci-lint / oapi-codegen / gci / gofumpt）
make hooks-install       # 接入 .githooks/pre-commit（每 clone 一次）
cp .env.example .env
```

依赖（Postgres + Redis）二选一：

- **A** docker compose 路径：`make dev-up`
- **B** 宿主进程路径：装好 PG/Redis 后 `make dev-deps-check` 探活

两条路径的详细命令见 [runbook §一次性环境准备](./runbook.md#一次性环境准备)。

初始化 DB + 起服务：

```sh
make run-migrate         # 跑迁移 up（应用 migrations/ 待执行的 SQL，建表）
go run ./cmd/api         # :3000
go run ./cmd/worker      # 另一终端跑 Asynq 消费
```

> 这是 skeleton 仓库本身的 onboarding。如果你是**从 skeleton fork 出新服务**，先跑 `./scripts/rename.sh <new-module> <shortname>` 改 module 名（一次性，跑完删脚本）——详细 [runbook §0](./runbook.md#0-第一次复制-skeleton)。

---

## 二、日常循环：决定改什么

| 改什么 | 看哪一段 |
|---|---|
| 加 / 改 HTTP endpoint | [§三 加 endpoint](#三加一个-http-endpoint最高频路径) |
| 加 / 改异步任务 | [§四 加 Asynq 任务](#四加一个-asynq-异步任务) |
| 加错误码 | [§五 加错误码](#五加一个错误码) |
| 改 middleware / 加 metric / 改日志字段 | [§六 改基础设施](#六改基础设施middleware--metrics--log) |
| 改配置项 | [§七 加配置项](#七加配置项) |
| 改 DB schema | [§八 改 DB schema](#八改-db-schema) |

每条改动收尾都走同一条 [§九 提交前](#九提交前必跑) 路径。

---

## 三、加一个 HTTP endpoint（最高频路径）

按这个顺序，少一步都过不了 `make verify`：

1. **改契约**：在 [`api/openapi.yaml`](../api/openapi.yaml) 加 path + schema
2. **生成代码**：`make oapi`（产物 `internal/oapi/oapi.gen.go` **入库**，不要手改）
3. **拷模板**：`make new-endpoint NAME=<Name>`（如 `make new-endpoint NAME=Order`）
   - ⚠️ 跑完**编译不过**，因为新 service 文件引用了还没生成的 oapi 类型
4. **装配**：在 `internal/server.go::newHTTPHandlers` 把 `repository → service → handler` new 出来
5. **注册路由**：在 `internal/router/router.go::Dependencies` + `register*Routes` 挂路径
6. **写测试**：handler / service / repository 各加一个 `_test.go`（见 [§十 测试](#十测试))

详细分层规则：[CLAUDE.md::分层规则](../CLAUDE.md)。

**禁忌**：
- handler 写业务规则 → 挪到 service
- service 收 `*gin.Context` → 改成 `context.Context`
- service / handler 直接 import `gorm.io/gorm` → 通过 service 包里的 repository 接口隔离

---

## 四、加一个 Asynq 异步任务

1. **定义任务类型** 在 [`internal/task/`](../internal/task/) 新建文件：类型常量 + payload struct + `New<Name>Task` 工厂（带 `MaxRetry` / `TaskID` 等 Option）
2. **消费端 handler** 在 [`internal/worker/handler.go::RegisterHandlers`](../internal/worker/handler.go) 注册：业务逻辑委托 service，**不要**在 worker 包重复实现
3. **API 端入队**：service 通过 `ExampleQueue` 风格的接口依赖 `taskqueue.Queue`，不直接拿 `*asynq.Client`
4. **幂等约定**：用 `asynq.TaskID(...)` 绑业务唯一键，消费端 handler 第一步查"是否已处理"——详细见 [runbook §业务侧幂等约定](./runbook.md#业务侧写新任务时的幂等约定)

死信处理 / 观测：见 [runbook §Asynq 死信](./runbook.md#asynq-死信--投毒任务处理)。

---

## 五、加一个错误码

1. 在 [`pkg/errcode/common.go`](../pkg/errcode/common.go) 加一行：`MyError = newError(NNN, "MY_ERROR")`
2. 在 [`pkg/response/response.go::MessageFor`](../pkg/response/response.go) 加对应 case
3. `make docs-errcodes` 重新生成 [`docs/errcodes.md`](./errcodes.md)

> 不用维护 `scripts/gen-errcodes.go` 里的清单——它走 AST 自动发现。

错误码段位约定：1000-1999 客户端错误，9000-9999 服务端错误。

---

## 六、改基础设施（middleware / metrics / log）

| 改什么 | 在哪 | 注意 |
|---|---|---|
| 加 gin middleware | [`internal/middleware/`](../internal/middleware/) | 错误响应走 `response.WriteValidationError` / `response.ErrorResponse` |
| 加 Prometheus 指标 | [`pkg/metrics/`](../pkg/metrics/) | 不要 import `prometheus.DefaultRegisterer`；用包内 Registry |
| 加 zap 日志字段 | 业务代码内 `applog.FromContext(ctx)` | 字段列表见 [runbook §日志字段速查](./runbook.md#日志字段速查) |
| 改装配顺序 | [`internal/server.go::newEngine`](../internal/server.go) | middleware 顺序有意义：TraceLogger → metrics → Recovery → ... |

> Worker / API 共用的资源（DB / Cache / Auth / Queue / Inspector）挂在 `bootstrap.Registry`，新增基础设施同步改 `Registry.Close()`。

---

## 七、加配置项

1. 在 [`config/types.go`](../config/types.go) 对应 sub-config 加字段
2. 在 [`config/config.go::Load`](../config/config.go) 用 `intEnv` / `boolEnv` / `durationEnv` / `int64Env` 等 helper 读 env
3. 必要时在 [`config/validate.go`](../config/validate.go) 加约束
4. 同步更新 [`.env.example`](../.env.example)——`make verify` 链里的 `env-verify` 会用 `go/ast` 扫 `config/*.go` 里的 `Getenv` / `intEnv` / `boolEnv` 等 helper 调用，与 `.env.example` 双向校验，漏改会硬红

---

## 八、改 DB schema

用 [goose](https://github.com/pressly/goose) 跑版本化 SQL 迁移（真相源是 [`migrations/`](../migrations/) 下的 `*.sql`，**不是** Go struct；`AutoMigrate` 已移除）：

1. `make migrate-create name=add_xxx` 生成时间戳前缀的空迁移文件
2. 在该文件里填 `-- +goose Up` / `-- +goose Down` 两段 SQL（DDL 自己写，可删列 / 改类型 / 数据回填）
3. 改 [`internal/model/`](../internal/model/) 的 struct 让 GORM 运行时映射对得上（struct 与迁移文件需手动保持一致）
4. `make run-migrate` 应用；回滚一版 `make migrate-down`，看状态 `make migrate-status`

> 命令详解（含 `-cmd up/down/status`、命名约定）见 [runbook §本地起完整三进程](./runbook.md#本地起完整三进程)。和 AutoMigrate 不同，破坏性变更（删列 / 改类型）现在由你显式写在 Down/Up 里，goose 不会替你跳过。

---

## 九、提交前必跑

```sh
make verify              # fmt + vet + test + lint + architecture-verify + env-verify + tidy-verify + oapi-verify + docs-verify + docs-deploy-check + docs-errcodes-verify（每步打横幅）
```

红了看最后一个 `=== STEP FAILED: xxx ===` 横幅指向的步骤；详细排错见 [runbook §排错 cheat sheet](./runbook.md#排错-cheat-sheet)。

**不要用 `--no-verify` 跳过 pre-commit hook**——hook 自身只跑 `fmt + vet` + .env 拦截，不挂全量 verify。挂了就是真问题，不要绕过。

---

## 十、测试

| 层 | 工具 | 模板 |
|---|---|---|
| service / handler / middleware | 标准库 `testing` + `httptest` + inline mock | [`internal/service/example_test.go`](../internal/service/example_test.go) |
| repository (DryRun) | GORM `DryRun` 捕获 SQL | [`internal/repository/example_test.go`](../internal/repository/example_test.go) |
| repository (真实 DB) | `//go:build integration` + 真 PG | [`internal/repository/example_integration_test.go`](../internal/repository/example_integration_test.go) |

**硬约束**（见 [CLAUDE.md::测试约定](../CLAUDE.md)）：
- ❌ 不引入 testify / gomock / mockery / sqlmock / testcontainers
- ✅ mock 是 inline struct + func 字段，就近放在测试文件里
- ✅ 测试 `init()` 调 `applog.SetLogger(zap.NewNop())` 静音日志
- ✅ handler 测试 `init()` 调 `validator.InitValidator()` 初始化 binding

跑：

```sh
go test ./...                               # 单元测试
make test-race                              # race detector
make test-integration                       # 集成（需要 dev-deps-check 通过）
make cover && open coverage.html            # 覆盖率
```

---

## 十一、Commit + PR

**Commit message** 格式（详见 [CLAUDE.md::Commit message](../CLAUDE.md)）：

```text
type(scope): description

- 改了什么
- 为什么改
```

- type: `feat / fix / refactor / docs / test / chore / style / ci`
- scope: 受影响的模块名（`api / worker / handler / service / repository / middleware / cache / log / response / errcode / oapi / build / ci / docs / deps`）

**分支策略**：
- 主干 `master`，**不**直接 push
- `feat/xxx` / `fix/xxx` / `refactor/xxx` → 开 PR
- CI（`make verify` + race + integration + systemd-analyze）全绿才合
- 不 force push 主干

---

## 十二、CI 会做什么

| Workflow | 触发 | 内容 |
|---|---|---|
| [`.github/workflows/ci.yml`](../.github/workflows/ci.yml) | push / PR | `make verify` + race + integration + systemd-analyze |
| [`.github/workflows/security.yml`](../.github/workflows/security.yml) | 每周一 + 手动 | `make sec`（govulncheck + gosec） |
| [`.github/workflows/release.yml`](../.github/workflows/release.yml) | tag `v*` | 跨平台构建 + Release tarball + SHA256SUMS |
| [`.github/dependabot.yml`](../.github/dependabot.yml) | 每周 | go_modules / github_actions / docker 三个 ecosystem |

---

## 十三、出问题怎么办

| 现象 | 看哪 |
|---|---|
| `make verify` 某步红 | [runbook §排错 cheat sheet](./runbook.md#排错-cheat-sheet) |
| `oapi-verify` out of sync | `make oapi && git add internal/oapi/oapi.gen.go` |
| 测试日志刷屏 | 测试 `init()` 漏 `applog.SetLogger(zap.NewNop())` |
| 端口占用 | `make stop-api` 或换 `API_PORT=3001 make run-api` |
| 线上 P0 | [runbook §线上 P0 排错速查](./runbook.md#线上-p0-排错速查) |
| 性能问题 | [runbook §pprof](./runbook.md#线上排障开-pprof) |
| Worker 不消费 | Asynqmon UI + [runbook §Asynq 死信](./runbook.md#asynq-死信--投毒任务处理) |

---

## 十四、Release

走 git tag 触发 CI：

```sh
git tag v0.x.y && git push origin v0.x.y
```

[`release.yml`](../.github/workflows/release.yml) 跨平台编译 + 打 tarball + SHA256SUMS 上传到 GitHub Releases。

二进制部署到目标主机：见 [`docs/deploy.md`](./deploy.md)（systemd unit / 滚动升级 / 回滚 / 日志查询）。
