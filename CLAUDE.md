# go-skeleton 项目约定

> 给 Claude 看的项目专属规则。全局规则见 `~/.claude/CLAUDE.md`，这里只列本项目特有的约定。
>
> **与 `AGENTS.md` 保持同步**：本项目并行维护两份 AI 编码助手规则文件（CLAUDE.md 给 Claude Code、AGENTS.md 给 Codex 等其他工具）。改一份时另一份也要改，否则两个助手会给出漂移的代码。

## 技术栈

Go 1.26+ + Gin + GORM + PostgreSQL + Redis + Asynq。模块名 `go-skeleton`。

## 顶层目录

| 目录 | 职责 | 写代码时的硬约束 |
| --- | --- | --- |
| `cmd/api` `cmd/worker` `cmd/migrate` | 三个独立进程入口 | `main.go` 只做"加载配置 → 初始化 → 启动 → 信号优雅关闭"，禁止写业务逻辑 |
| `config/` | 环境变量加载、运行时配置 | 不持有业务逻辑；新增配置项要补 `.env.example` |
| `internal/bootstrap/` | 进程级资源装配，输出 `Registry` | API/Worker/Migrate 各自调对应的 `InitXxx` |
| `internal/` (package `app`) | `server.go` / `worker.go` 把 `Registry` 装配成完整调用链 | 所有 handler/service/repository 的 `new` 集中在这里 |
| `internal/router/` | URL → handler 映射 | 不构造依赖、不做初始化 |
| `internal/handler/` `service/` `repository/` `model/` | 分层业务代码 | 见下文"分层规则" |
| `internal/middleware/` | Gin 中间件 | 错误响应走 `response.ErrorResponse` |
| `pkg/errcode/` | 业务错误码 | 只在这里定义新错误，不在 service/handler 内联构造 |
| `internal/task/` | Asynq 任务类型定义 | API 和 Worker 共享 |
| `internal/worker/` | Asynq 消费端的 handler 实现 | 复用 `service`/`repository`，不要重写业务逻辑 |
| `internal/taskqueue/` | Asynq client 的薄封装 | service 通过 `ExampleQueue` 这种接口依赖它，不直接 import asynq |
| `pkg/` | 跟业务无关的通用工具 | 严禁 import `internal/` 任何包 |

数据库迁移用 `cmd/migrate`（基于 [goose](https://github.com/pressly/goose) 库 API）跑仓库根目录 `migrations/` 下的版本化 SQL 文件，文件经 `//go:embed` 打进二进制。真相源是这些 SQL 文件、**不是** Go struct——`AutoMigrate` 已移除，改表结构走"`make migrate-create name=xxx` 生成空迁移 → 填 SQL → 跑 `make run-migrate`"。文件名是**时间戳前缀**（goose 时间戳风格）：`<YYYYMMDDHHMMSS>_<描述>.sql`，由 `make migrate-create` 自动生成、天然全局有序、多人并行不撞号；版本号必须是文件名首个 `_` 前的纯数字段，时间戳要连写、**不要**在中间插下划线（goose 解析不了）。命令：`make run-migrate`（up）/ `make migrate-down`（回滚一版）/ `make migrate-status`（看状态）/ `make migrate-create name=xxx`（新建空迁移）。迁移文件放仓库根 `migrations/`，**不要**塞 `internal/`。`cmd/migrate` 用 goose 的 `Provider` API（绑死 `DialectPostgres`，本项目**只支持 Postgres**）并配 Postgres advisory lock，多实例/多机并发跑 migrate 时自动串行化、不竞态。生产迁移要对旧代码**向后兼容**（只增不破坏），破坏性变更走 expand-contract 两阶段发布——详见 [docs/deploy.md](docs/deploy.md) 升级/回滚段。

迁移文件 lint 由 `migrations/migrations_test.go` 在 `make verify` 链里执行，强制三道门：(1) 文件名严格 `<14位时间戳>_<snake_case>.sql`；(2) 必须含 `-- +goose Up` 与 `-- +goose Down` 注解；(3) Up 段里 `DROP TABLE/COLUMN/CONSTRAINT`、`ALTER COLUMN TYPE/SET NOT NULL`、`RENAME COLUMN/TO`、`TRUNCATE` 这类破坏性 DDL 必须配 `-- breaking: <reason>`（或 `-- +breaking <reason>`）显式标注。新增危险 DDL 形态时同步更新 `migrations_test.go::dangerousDDL`。

## 分层规则：handler → service → repository

调用方向是单向的，不允许反向依赖，不允许越级。

### handler
- 只做三件事：参数绑定、调 service、格式化响应。**不写业务规则。**
- 参数校验失败走 `response.BuildValidationErrorResponse(c, err)` → `c.JSON(200, ...)`。
- service 返回 error 时统一走 `response.WriteError(c, err)`，由它根据 `errcode.Error` 还是兜底来转协议。
- 成功响应走 `response.WriteSuccess(c, data)`。

### service
- 入参用 `context.Context`，**禁止用 `*gin.Context`**。Worker 也消费 service，绑死 gin 会让 Worker 跑不通。
- 返回 `errcode` 包里的错误值（如 `errcode.DatabaseError`），不要返回拼接字符串、不要在 service 里调 `c.JSON`。
- 业务流程编排可以跨多个 repository / queue / cache；不能直接写 GORM 链式调用。
- 依赖通过构造函数注入，**不要在 service 内部 `new` 其他 service / repository**。
- 依赖接口（如 `ExampleRepository`、`ExampleQueue`）就近定义在 service 包里，方便测试 mock。

### repository
- **唯一允许写 GORM 或原生 SQL 的层**。其他层禁止 import `gorm.io/gorm`。
- 所有查询都用 `db.WithContext(ctx)`，禁止 `context.Background()` 替换。
- 走事务时用 `repository.InTx(ctx, db, fn)` + `dbFromContext(ctx, r.db)`，让上层组合事务。需要自定义隔离级别 / 只读事务用 `repository.InTxWithOptions(ctx, db, *sql.TxOptions, fn)`（如分页强一致 total 走 `sql.LevelRepeatableRead` + `ReadOnly`）；嵌套调用 opts 会被忽略，isolation 必须在最外层定。

### model
- 纯 GORM 数据结构，不挂带业务规则的方法。复杂行为放 service。

### 何时引入 usecase 层
不要预先架空。只有当一次操作要协调 ≥3 个 service / 跨领域时再考虑加 `usecase`。当前骨架不需要。

## 依赖装配（手写 DI）

- 不引入 Wire / Dig / Fx。装配集中写在 `internal/server.go` 的 `newHTTPHandlers` / `newEngine` 里。
- `bootstrap.Registry` 持有跨进程共享资源：`Cfg`、`DB`、`Cache`、`Auth`、`Queue`。新增基础依赖时挂到 `Registry`，并在 `Close()` 里补关闭逻辑。
- handler / service / repository **声明依赖、不构造依赖**。新增模块的标准动作：
  1. 在对应分层包里加 `NewXxx(...)` 构造器；
  2. 在 `internal/server.go` 的 `newHTTPHandlers` 里组装；
  3. 在 `internal/router/router.go` 的 `Dependencies` 里加字段，并在 `registerXxxRoutes` 注册路由；
  4. Worker 需要的话在 `internal/worker.go` 的 `buildWorkerDeps` 里挂依赖、`internal/worker/handler.go` 里注册 Asynq handler。

## 统一响应协议

所有业务 API 返回统一信封，结构定义在 `pkg/response/response.go`：

```json
{ "code": 0, "message": "success", "data": { ... } }
{ "code": 1001, "message": "...", "reason": "INVALID_PARAMS", "metadata": { "trace_id": "..." } }
```

字段名都用完整单词（`message` / `reason` / `metadata`），不引入简写。需要新增响应字段时同理。

**HTTP 状态码按 errcode 映射**（由 `errcode.Error.HTTPStatus()` 决定，pkg/response 的 `WriteError` / `WriteValidationError` 自动应用）：

- 成功（`code=0`）→ 200
- 1xxx 客户端错误段位 → 400 / 401 / 403 / 404 / 408 / 429 / 503（按 reason 精确映射）
- 9xxx 服务端错误段位 → 500 / 501 / 503

完整映射表见 [`docs/errcodes.md`](./docs/errcodes.md)（由 `make docs-errcodes` 生成）。客户端仍以 **body `code`** 做精确业务分支；HTTP status 给监控 / LB / 透明代理用作粗粒度信号，两者互不替代。

例外：`/livez` 与 `/health` **不走信封**，直接返 200 / 503 给 K8s 探针；`/livez` 是 liveness（永远 200），`/health` 是 readiness（依赖不可用时 503）。

新增错误码：(1) 去 `pkg/errcode/common.go` 加 `newError(code, "REASON")` 常量；(2) 在 `pkg/response/response.go::MessageFor` 补默认英文文案；(3) 如果新 reason 应映射到 HTTP 段位之外的特定 status，去 `pkg/errcode/type.go::HTTPStatus` 的 switch 加 case + 配套单测；(4) 跑 `make docs-errcodes` 重新生成 `docs/errcodes.md`。

## i18n

**当前未实现**。如果未来加，按下面这套约定来，不要散落到各层：

- 翻译文件统一放 `config/i18n/locales/{lang}.json`，key 用 `errcode` 的 `Reason`（如 `INVALID_PARAMS`）。
- 在 `pkg/response` 的 `MessageFor` 内根据 `Accept-Language` 切换文案，handler 和 service **不**自己拼语言相关字符串。
- `pkg/validator` 的 binding 错误翻译也走同一套机制，不要硬编码中文。

## JWT 鉴权

`pkg/auth.JWTManager` 当前实现 **Layer 1（HS256 签名 + exp + iss 校验）** + `pkg/auth/jwt.go` 的 Bearer 解析；`middleware.BearerAuth` 把 `Subject` 写到 `gin.Context` 的 `"auth_subject"` 键。

未实现（需要时再加，**不要默认启用**）：
- Layer 2：Redis JTI 黑名单（解决主动登出）。
- Layer 3：`token_version` 比对（解决批量失效，比如改密码踢人）。

加 Layer 3 时引入 `TokenVersionStore` 接口注入到 `JWTManager`，让不需要的项目不付额外成本。

## 异步队列

- 选 Asynq，理由：复用 Redis，自带 Scheduler 和 Asynqmon。**不要引入 Kafka / RabbitMQ。**
- 任务类型常量和 payload 定义放 `internal/task/`，API 和 Worker 共享。
- **所有 payload struct 必须头部匿名嵌入 `task.Header`**（带 `Version` + `TraceID`），通过 `task.NewHeader(traceID)` 构造。worker handler 反序列化后第一时间调 `task.CheckHeader(p.Header, task.CurrentSupported)`——schema 不兼容时返 error 走 retry，**不要静默吞**（吞了等于丢消息）。改 payload 字段语义 / 删字段必须升 `task.PayloadSchemaVersion` 并同步更新 `CurrentSupported`；新增字段 + omitempty 不用升版本。
- **新建 task 走 `task.DefaultOptions()`**（含 MaxRetry=5、Timeout=30s）而不是各工厂自己写一份 `asynq.MaxRetry(...)`。业务有长任务 / 特殊重试需求时显式 `append` 覆盖。
- **业务键稳定的 task 用 `asynq.TaskID(task.BuildTaskID("ns", keys...))`** 做永久全局去重（订单状态机推进、用户操作日志）。`BuildTaskID` 对超长 ID 会保留可读前缀并追加 SHA-256 后缀，避免简单截断导致不同长业务键误去重；命中 1KB 上限通常说明 caller 把过大的业务对象当 key，应回头收敛 key。**短窗口防抖**用 `asynq.Unique(ttl)`（用户点按钮、定时拉取）。两套语义不同，不要混用——TaskID 冲突返 `ErrTaskIDConflict`、Unique 重复返 `ErrDuplicateTask`，业务上对幂等的预期差很多。
- service 通过 `ExampleQueue` 接口依赖 `taskqueue.Queue`，不直接拿 `*asynq.Client`。
- Worker 消费端 handler 在 `internal/worker/handler.go` 注册，业务流程委托给 service。
- Worker 停服走两阶段：`srv.Stop()` 停止接新任务、`srv.Shutdown()` 等当前任务完成（已在 `internal/worker.go` 实现，扩展时不要破坏顺序）。
- Worker 启动用 `asynq.Server.Start`（同步、返回启动期 error），**不要退回 `server.Run`**。`Run` = `Start` + 它内置的 `waitForSignals` + `Shutdown`，那条内置 signal loop 会和 `cmd/worker/main.go` 的 `signal.NotifyContext` 抢 SIGTERM；而且 `Run` 异步起 goroutine 没法精确知道启动成败，会让 sd_notify `READY=1` 早发。`internal/worker.go` 的 `Run(ctx, onReady)` 已经是：`Start` 成功后才回调 `onReady`、停服由传入的 `ctx` 驱动。
- **Production 漏注入业务 processor 必须 fail-fast**。`internal/worker.go::buildWorkerDeps` 在 `APP_ENV=production` 且没真注入业务 processor（当前以 `reg.DB == nil` 为信号）时返 error，让 `NewWorker` 启动期失败。dev / staging 仍允许 noop 兜底，方便从模板态启动；但生产环境消息被 noop 消费 + ack 掉，比 panic 更危险（消息消失但只剩 warn 日志），所以宁可起不来也不能静默。

## context 传递（硬约束）

请求级 `context.Context` 从 handler 一路传到 repository，**业务层禁止用 `context.Background()` 替换**。它带着 `trace_id`、超时、取消信号，断了会导致：HTTP 已超时但 DB 查询还在傻跑。

写测试可以用 `context.Background()`，业务代码不行。

## 环境变量

- **入库模板只有根目录 `.env.example` 一份**，所有进程共用。新增配置项必须同步更新它。
- 运行时加载顺序（在 `cmd/<proc>/main.go` 里）：
  ```
  真实环境变量 > cmd/<proc>/.env（如果存在） > 根目录 .env
  ```
  本地想给某个进程开"差异化覆盖"时，自行手写 `cmd/<proc>/.env`——它不入库，被 `.gitignore` 兜底。
- 所有 `.env*`（除 `.env.example`）都在 `.gitignore`，禁止把真实凭证落到仓库。
- `APP_ENV`（development / production，默认 development）控制启动期安全 guard 的严格度，**不等同于 `GIN_MODE`**。设 `production` 时 `config/validate.go` 会硬拦截不安全配置：`JWT_SECRET` 为占位值 / 空 / 短于 32 字节、`AUTH_DEV_TOKEN_ENABLED=true`、`GIN_MODE != release`、`LOG_FORMAT != json`，任一不满足进程直接 fail-fast 退出。"非致命但大概率漏配"的项走 `config.ProductionWarnings(cfg)` 集中输出 warn（由 `cmd/api/main.go` 启动时打）：`RATE_LIMIT_PER_MINUTE=0`、`TRUSTED_PROXIES` 空、`/metrics` 与业务同端口（建议设 `METRICS_ADDR` 拆独立 listener）、`PPROF_ENABLED=true` 且 `PPROF_ADDR` 非 loopback（公网暴露 pprof = heap/goroutine 泄露 + DoS 向量，应绑 `127.0.0.1` / `::1` + SSH 隧道）。新增"生产必须硬拦"的项加到 `validateProductionSecrets`，"非致命漏配 warn"加到 `ProductionWarnings`，不要散到各层。

## 审计日志

`middleware.TraceLogger` 默认开启，按 `AUDIT_LOG_EXCLUDE_PATHS` 排除路径（如 `/health`）。日志字段用 zap 结构化输出，敏感字段 (`Authorization`、`Cookie`、body 里的 `password` / `token`) 自动脱敏。**不要绕过中间件自己打日志带请求体。**

`trace_id` 从 `applog.TraceIDFrom(ctx)` 取；service / repository 打日志统一用 `applog.FromContext(ctx).Xxx(...)`，不要 `applog.L()` 全局 logger 打业务日志。

中间件同时把 `trace_id` 写到响应头 `X-Request-ID`，客户端传入相同头会被复用。

## pkg/ 边界

`pkg/{auth,cache,database,errcode,log,response,validator}` 是通用工具。改这些包之前确认：

- **严禁** import `internal/` 下任何包。`pkg` 内部互相 import 合法（`pkg/response` 依赖 `pkg/errcode` 就是这样）。
- 接口稳定优先于功能堆叠，因为理论上可以被其他项目复用。

## AI 助手提示（最高频违反，每次进项目先扫这段）

下面这些规则**写得很明白，但 AI 助手仍然会犯**。开始任何写代码任务前先内化这几条，可以省下大量返工：

- **不要修改 `internal/oapi/oapi.gen.go`**。它顶部标了 DO NOT EDIT，唯一改它的方式是改 `api/openapi.yaml` 然后 `make oapi`。哪怕只是改一行 import / 注释 / 字段名都会被 oapi-verify 抓出来。
- **`oapi.Example`、`oapi.CreateExampleReq` 等业务实体类型不要 import**。业务结构以 `internal/service` 包为准（如 `service.CreateExampleReq`）；只有协议层 schema（`oapi.HealthResponse` / `oapi.LivenessResponse` / `oapi.ListExamplesParams` 等）可以直接用。
- **service 入参永远是 `context.Context`，不是 `*gin.Context`**。Worker 也消费 service，绑死 gin 会让 Worker 跑不通。需要 trace_id / auth subject 这种字段，由 handler 提前从 `*gin.Context` 取出来，作为 primitive 传给 service。
- **测试不要引入 testify / gomock / mockery / sqlmock / testcontainers**。本项目坚持标准库 `testing` + 手写 mock，参考 `internal/service/example_test.go`。
- **响应字段是 `message`，不是 `msg`**（早期用过 `msg`，已经统一改成 `message` 这种完整单词；不要又退回简写）。
- **错误返回值用 `pkg/errcode` 里的常量**，不要 `fmt.Errorf("...")` 字符串拼接。底层错误用 `applog.FromContext(ctx).Error(..., zap.Error(err))` 单独记日志。

## 写代码时常犯的错（已知会触发返工）

- ❌ 在 handler 写业务规则 → ✅ 挪到 service。
- ❌ service 收 `*gin.Context` → ✅ 收 `context.Context`，需要的字段由 handler 传 primitive。
- ❌ repository 之外的层 import `gorm.io/gorm` → ✅ 通过 service 包里定义的接口隔离。
- ❌ 用 `fmt.Errorf("xxx")` 直接返 → ✅ 返 `errcode.XxxError`；底层错误 `applog.FromContext(ctx).Error(..., zap.Error(err))` 记进日志。
- ❌ 在 service 里 `context.Background()` 起新 ctx 调 DB → ✅ 传原 ctx；只有 fire-and-forget 后台任务才允许，且必须独立带超时。
- ❌ 给 Worker 复制一份和 API 不同的业务逻辑 → ✅ 共享 service。

## API 契约：OpenAPI 3.1

**真相源是 `api/openapi.yaml`**。新增 endpoint 走 yaml 驱动：

1. 改 `api/openapi.yaml`：加 path + schema。operationId 用驼峰命名（如 `listOrders / createOrder / getOrder`，与 oapi-codegen 生成的 `ServerInterface.<Method>` 对齐）。需要鉴权的 op 加 `security: [{ bearerAuth: [] }]`。资源归属用 `x-resource: <Name>`（推荐）—— path 级声明一次下面所有 verb 继承，operation 级可覆盖；不加 `x-resource` 时 fallback 到"operationId 大小写不敏感包含 NAME"老逻辑。动作名推不出来时加 yaml extension `x-handler-method: <Action>` 显式指定。
2. `make oapi` 重新生成 `internal/oapi/oapi.gen.go`。
3. `make new-endpoint NAME=<Name>` —— 脚本按 yaml 资源归属生成 handler / service / repository / model / task 五层骨架 + 三个测试模板，并注入 `internal/server.go` 装配链、`internal/router/router.go` 路由（按 yaml `security` 推 `deps.AuthRequired` 子组）、`internal/handler/openapi.go::APIServer` 字段 + 转发方法。生成的 service / repository 方法返 `errcode.NotImplementedYet`（9005）—— **仓库立即可以 `make verify` 通过**，填业务时换 nil 或具体错误码。**review 用 `--dry-run` / `DRY_RUN=1` 只打印计划不写盘**。
4. 填业务：handler 补 `c.ShouldBind...`、service 填业务规则换掉 NotImplementedYet、repository 写 SQL、model 补字段。
5. `make verify` 通过——`oapi-verify` 会用 `git diff --quiet` 检查生成产物已 commit。

**编译期保险线**在 `internal/handler/openapi.go` 里的：
```go
var _ oapi.ServerInterface = (*APIServer)(nil)
```
yaml 和代码一旦漂移，**build 直接失败**，不依赖人去 review 注释。

**直接手改 `internal/server.go` / `router.go` / `handler/openapi.go` 的注入区是禁忌**——那些块由 `// NEH ...` 锚点界定，下次跑 `make new-endpoint` 会按锚点继续注入。改 yaml 重跑而不是手编辑那几个文件。

### `make new-endpoint` 支持边界

支持的形态：

- **资源归属**：`x-resource: <Name>`（推荐 path 级，operation 级可覆盖）。没声明时 fallback 到 operationId 包含 NAME——歧义场景（如 `NAME=Order` 命中 `listOrderPayments`）请改用 `x-resource` 显式声明。
- **handler 动作名**：默认 operationId 去掉 NAME 后剩余 + 首字母大写；推不出（剩余为空 / 非法）时报错，加 `x-handler-method: <Action>` 显式指定。
- **path 参数**：0 或 1 个 `{var}`。参数名按 yaml 实际取（`{order_id}` → `c.Param("order_id")` + service `(ctx, order_id string)`）。
- **鉴权**：yaml `security: [{ bearerAuth: [] }]` 自动放进 `deps.AuthRequired` 子组；非 bearerAuth 的 scheme（API key / OAuth2 / ...）当前忽略。
- **dry-run**：`--dry-run` / `DRY_RUN=1` 跑解析 / 校验但不写盘——review 计划用。
- **drift 检查**：`make new-endpoint-check` 只读 checker，重新解析 yaml 比对代码端，按 `[!] Missing` / `[~] Stale` / `[-] Mismatch` 三档报漂移。**不写盘 / 不删代码**。传 `NAME=Order` 只扫单资源；不并入 `make verify`（避免 schema 调整让 PR 抖动），单跑作为调试入口。
- **DTO 反推（可选）**：`--dto` / `DTO=1` 时从 yaml schema 反推 service 包内的请求 DTO struct（如 `CreateOrderReq`）+ handler 自动 `ShouldBindJSON` / `ShouldBindQuery` + service 签名同步换成 `(ctx, *XxxReq)`。**默认关**——避免一刀切改变骨架行为。仅支持简单 object schema（顶层 string / integer / boolean + required + min/max/minLength/maxLength）；遇到 allOf / oneOf / anyOf / 嵌套 object / array / enum / $ref 时降级到空 struct + `// TODO` 注释，让作者照 yaml 手写。query DTO 用 `form:` tag（gin `ShouldBindQuery` 不看 json）；body DTO 用 `json:` tag。

不支持，需要手写的形态：

- **≥2 个 path 参数**（如 `/users/{uid}/orders/{oid}`）：脚本 fail-fast。加 `x-handler-method` 拿到方法名后手写 handler / router。
- **同一资源跨多个根路径**：脚本按 `x-resource` 拉到一起没问题；但 `r.Group` 路径取 ops 的最长公共前缀，多根路径时只能用最长公共部分，子路径走 `g.GET("/sub/...", ...)`——少见，能跑但不优雅。

### 单一真相约定（重要）

oapi-codegen 会生成两类东西，**区分对待**：

- **协议层 schema**（`oapi.HealthResponse`、`oapi.LivenessResponse`、`oapi.HealthResponseChecks`、`oapi.ListExamplesParams`、`oapi.BearerAuthScopes` 等）—— handler **应该**直接用，让响应/参数结构跟 yaml 强对齐。
- **业务实体**（`oapi.Example`、`oapi.CreateExampleReq` 等业务请求/响应模型）—— handler / service / repository **不要** import；业务结构以 `internal/service` 包为准（如 `service.CreateExampleReq`、`service.ExampleService` 返回的 `*model.Example`）。

判断方法：如果生成的类型只服务于 transport 层（响应外壳、query params、security scope），用它；如果它承载业务字段（id/name/created_at 这种领域属性），不用它。

`internal/oapi` 包对外的关键导出：
- `oapi.ServerInterface`：用于编译期契约保险。
- `oapi.GetSpecJSON()`：用于 `/openapi.json` 返回 embedded spec。
- 协议层 schema 类型：handler 可以直接用。

`internal/oapi/oapi.gen.go` 顶部标了 `DO NOT EDIT`——**不要手改它**，改 yaml 然后 `make oapi`。它会入库（和 `go.sum` 一样），CI / 队友不需要重跑生成。

### yaml 文档语言

`api/openapi.yaml` 的 `summary` / `description` 字段**统一用中文**——会被 `/docs`（Stoplight Elements）渲染成 Markdown 给团队浏览，中文更易读。命名相关字段保持英文：

- ✅ 中文：`summary`、`description`（path-level / operation-level / parameter / requestBody / response / schema / property / tag）
- ✅ 英文：`operationId`、字段名（key）、`reason` 常量串（`INVALID_PARAMS` 等需要稳定机读，**不译**）
- `summary` 写**短中文标题**（左侧菜单和列表项标题），长说明放 `description`
- `description` 支持 Markdown，可用列表 / 代码块 / 加粗；**别用 `#` / `##`** 一级二级标题（会与 Elements 自带层级冲突）
- 错误码 `reason` 字符串（如 `INVALID_PARAMS`）保留英文常量，中文说明放周边 description 里

### 不做的事

- 不生成 client SDK（内部联调用不到）。
- 不生成 strict-server wrapper（会绕开 Gin 上下文，破坏现有 middleware）。
- 在线文档用 Stoplight Elements（纯 CDN web component，零 Go 依赖、不接管路由）挂在 `/docs`，spec 复用 `/openapi.json`；页面外观由启动期 `DOCS_*` env 配置（title / theme / layout / hide_try_it / hide_schemas / logo，见 `.env.example`），在 `handler.NewOpenAPIHandler` 里一次性预渲染、运行时不变。`/docs` 与 `/openapi.json` 只在非生产环境注册（`internal/server.go` 的 `newEngine` 里 `!reg.Cfg.Env.IsProduction()` 守卫），生产环境隐藏 API 契约与文档 UI、访问得 404。前端也可继续用 `/openapi.json` 导入 Postman / Bruno / Insomnia。仍**不引入** `swaggo/swag`、`gin-swagger` 这类把 UI 框架编译进 Go、或用 `RegisterHandlers` 接管路由的重型方案。
- 不用 `oapi.RegisterHandlers` 接管路由注册——业务路由仍走 `internal/router`，享受细粒度中间件控制（如 `/auth/me` 要 BearerAuth、`/auth/token` 不要）。

### oapi-codegen 对 OpenAPI 3.1 的支持

oapi-codegen 当前对 3.1 标注 "partial support"，跑生成会打 WARNING。本项目实测可用；如果未来某个 3.1 特性导致生成失败，**先看 yaml 能不能用 3.0 兼容写法表达**，不要回退到 3.0。

## 验证命令

声明任务完成前必须跑过：

```sh
make verify   # fmt + vet + test + lint + architecture-verify + env-verify + tidy-verify + oapi-verify + docs-verify + docs-deploy-check + docs-errcodes-verify（每步打横幅，便于定位失败）
```

需要单独跑某一项时见 `make help`。详见根目录 `README.md` 的 "Verify" 小节。

**常用命令速查见 [`docs/runbook.md`](./docs/runbook.md)**——它把"新增 endpoint / 新增任务 / 跑特定测试 / 排错"等高频动作整理成可执行清单，AI 助手优先读它。

**二进制部署见 [`docs/deploy.md`](./docs/deploy.md)**——主机初始化、systemd unit 安装、滚动升级、回滚、日志查询；与 Docker 路径并存。

## 测试约定

项目目前的测试风格是**标准库 `testing` + 手写 mock**，下面这些约定是为了让新写的测试和老的一脉相承，不要凭直觉换风格。

### 工具栈

- ✅ 用 `testing`、`net/http/httptest`、`errors.As`、`gorm.io/gorm` 的 `DryRun`。
- ❌ **不引入 testify / gomock / mockery / sqlmock / testcontainers**。如果觉得不够用，先在 PR 描述里说服别人，再加依赖。

### 测试文件位置

和被测代码同包同目录，文件名 `xxx_test.go`。**不要**单独建 `test/` 顶层目录、不要把 mock 拆到 `mocks/` 子目录。每个测试文件内部就近放 mock 类型，方便阅读。

### 各层测试模式（沿用现有写法）

| 层 | 怎么测 | 参考文件 |
| --- | --- | --- |
| `service` | inline struct + func 字段实现依赖接口，注入 `NewXxxService(...)` | `internal/service/example_test.go` |
| `handler` | `gin.SetMode(gin.TestMode)` + `httptest.NewRecorder`，反序列化 `response.Response` 断言字段 | `internal/handler/example_test.go` |
| `repository` | `gorm.Open(postgres.Open(...), &gorm.Config{DryRun: true, DisableAutomaticPing: true})` + GORM callback 捕获 SQL，**不连真实 DB** | `internal/repository/example_test.go` |
| `middleware` | 同 handler，构造 `gin.Engine` + 单条路由 | `internal/middleware/auth_test.go` |
| `pkg/auth` 等基础包 | 纯单元测试，覆盖正反两面 | `pkg/auth/jwt_test.go` |

### 必须遵守的细节

- **测试里的日志静音**：在 `init()` 调 `applog.SetLogger(zap.NewNop())`。否则跑 `go test ./...` 会刷一堆 audit log。handler 测试还要 `validator.InitValidator()`，否则 binding 校验报错文案是空的。
- **错误断言走 `errcode`**：用 `errors.As(err, &ec)` 拿 `errcode.Error`，比对 `ec.Code() == errcode.XxxError.Code()`。**不要**用 `err.Error() == "..."` 比较字符串。
- **mock 命名**：包内未导出，`mockXxx` 驼峰；持 func 字段而不是写一堆条件分支：

  ```go
  type mockExampleRepo struct {
      createFunc func(ctx context.Context, example *model.Example) error
  }
  func (m *mockExampleRepo) Create(ctx context.Context, e *model.Example) error {
      return m.createFunc(ctx, e)
  }
  ```

- **trace_id 注入**：handler 测试如果要验证 `metadata.trace_id`，用一个 `gin.HandlerFunc` 提前 `c.Set("trace_id", "test-trace")`，别去 mock 整套 TraceLogger。
- **`context.Background()` 在测试里允许**，业务代码里不行（见上文 context 传递）。
- **表驱动**：测试用例多于 3 个时用 `t.Run(name, ...)` + 切片表。同一个行为正反两面的 case 用独立 `TestXxx` 函数也可以，本项目两种都有，按可读性挑。

### 跑测试

```sh
go test ./...                         # 全跑
go test ./internal/service/... -run TestCreate -v    # 单包 + 单测
go test ./... -race                   # 带 race（CI 已开自动，写并发代码时本地也可手动跑）
go test ./... -cover                  # 看覆盖率
```

**不要给跑不通的测试加 `t.Skip()` 蒙混过关。**测试坏了就修，或者删——不要假装绿。

## Git Workflow

仓库已初始化，主干 `master`，远程 `origin` = `https://github.com/myxiaoao/go-skeleton`。

### Commit message

格式：`type(scope): description` + 空一行 + 详细变更说明，每项一行。type 用英文（feat/fix/refactor/docs/test/chore）。本项目的 scope 约定：

| Scope | 对应改动 |
| --- | --- |
| `api` | `cmd/api/`、`internal/server.go`、HTTP 路由 / middleware、`api/openapi.yaml` 契约 |
| `worker` | `cmd/worker/`、`internal/worker.go`、`internal/worker/`、Asynq handler |
| `migrate` | `cmd/migrate/`、迁移相关 |
| `bootstrap` | `internal/bootstrap/`、`config/` |
| `handler` / `service` / `repository` / `model` / `router` / `middleware` / `task` / `taskqueue` | 对应 `internal/*` 子包 |
| `auth` / `cache` / `database` / `errcode` / `log` / `response` / `validator` | 对应 `pkg/*` 子包 |
| `oapi` | OpenAPI codegen 配置 / 生成产物 (`api/oapi-codegen.yaml`、`internal/oapi/`) |
| `build` | `Makefile`、构建脚本 |
| `ci` | `.github/workflows/*`、`.github/dependabot.yml` |
| `docs` | README、AGENTS.md / CLAUDE.md、注释 |
| `deps` | `go.mod` / `go.sum` 调整 |

示例：

```text
feat(service): example 新增分页参数校验

- 在 ListExamplesReq 上加 omitempty,min=1,max=100 binding
- service 默认 limit 改成 20
- 补 TestListDefaultLimit / TestListInvalidLimit
```

### 分支策略

- 主干 `master`。功能分支 `feat/xxx`，修复 `fix/xxx`，重构 `refactor/xxx`。
- **不要直接 push 到 `master`**——走 PR。CI（`.github/workflows/ci.yml`）会跑 `make verify`，全绿才合。
- 不要 force push 主干。force push 任何分支前先确认本地不会丢工作。

### 提交前自检

每次 commit 前一条命令搞定：

```sh
make verify   # fmt + vet + test + lint + architecture-verify + env-verify + tidy-verify + oapi-verify + docs-verify + docs-deploy-check + docs-errcodes-verify
```

任意一项挂了**不要 `--no-verify` 跳过**——按通用规则，hook 失败先修问题再重新 commit，不要 amend。

### Stage 文件时的硬约束

**不要直接 `git add .`**——`.DS_Store`、临时文件可能漏网。逐项 `git add path/...` 或用 `git add -p`。`.env`、`bin/`、`dist/`、`coverage.out` 虽在 `.gitignore` 兜底，但 stage 时仍以"知道自己加进去什么"为准。

### 禁止入库

- 任何真实凭证：`cmd/*/.env`、`.env`、密钥文件。已在 `.gitignore`，**不要去改 `.gitignore` 放行**。
- 构建产物：`bin/`、`dist/`、`coverage.out`。已在 `.gitignore`。
- IDE 个人配置：`.idea/`、`.vscode/`（如确实要共享 VSCode 配置，单独讨论加白名单文件）。

## 目录树速查

```text
go-example/
├── .env.example                配置模板（真实 .env 不入库）
├── .gitignore
├── .dockerignore
├── Makefile                    开发与提交前一站式入口（make help 查全部 target）
├── README.md
├── AGENTS.md                   Codex 等 AI 编码助手的项目规则（与本文件并存维护）
├── CLAUDE.md                   本文件（Claude Code 用）
├── CHANGELOG.md                Keep a Changelog 格式
├── Dockerfile                  multi-stage 构建（默认 cmd/api）
├── docker-compose.yml          本地 Postgres + Redis
├── go.mod / go.sum             模块名 go-skeleton
│
├── api/                        API 契约层
│   ├── openapi.yaml            真相源：OpenAPI 3.1 spec
│   └── oapi-codegen.yaml       codegen 配置
│
├── migrations/                 版本化 SQL 迁移（真相源，go:embed 进二进制）
│   ├── embed.go                //go:embed *.sql → fs.FS
│   └── <时间戳>_*.sql          goose 时间戳风格（-- +goose Up / Down）
│
├── cmd/                        三个进程入口，main.go 只做启停
│   ├── api/main.go             HTTP 服务
│   ├── worker/main.go          Asynq 消费者
│   └── migrate/main.go         goose 迁移（up/down/status）
│
├── config/                     配置加载 + 类型定义
│   ├── config.go
│   ├── runtime.go              进程级运行时初始化(logger 等)
│   └── types.go
│
├── internal/                   business code（Go internal 约束）
│   ├── server.go               package app: HTTP 装配入口 (NewServer)
│   ├── worker.go               package app: Worker 装配入口 (NewWorker)
│   │
│   ├── bootstrap/              Registry 模式，进程资源装配
│   │   ├── registry.go         Registry struct + Close
│   │   ├── api.go              InitAPI
│   │   ├── worker.go           InitWorker
│   │   └── runtime.go          InitRuntime（logger 等）
│   │
│   ├── router/router.go        URL → handler 注册（不构造依赖）
│   │
│   ├── handler/                HTTP 协议适配层
│   │   ├── auth.go             /api/v1/auth/*
│   │   ├── example.go          /api/v1/examples/*
│   │   ├── health.go           /livez（liveness）+ /health（readiness）
│   │   └── openapi.go          /openapi.json + APIServer (满足 oapi.ServerInterface)
│   │
│   ├── service/                业务逻辑层（context.Context 入参）
│   │   └── example.go
│   │
│   ├── repository/             数据访问层（唯一允许写 GORM）
│   │   ├── example.go
│   │   └── tx.go               WithTx / InTx / InTxWithOptions / dbFromContext
│   │
│   ├── model/                  GORM 数据结构
│   │   └── example.go
│   │
│   ├── middleware/             Gin 中间件
│   │   ├── auth.go             BearerAuth + AuthSubject
│   │   ├── cors.go
│   │   ├── logger.go           TraceLogger（审计日志 + 脱敏 + X-Request-ID）
│   │   ├── rate_limit.go       IPRateLimiter
│   │   ├── recovery.go
│   │   └── timeout.go
│   │
│   ├── task/                   Asynq 任务类型定义（API 和 Worker 共享）
│   ├── taskqueue/              Asynq client 薄封装
│   │   └── taskqueue.go        Queue.Available / Enqueue
│   ├── worker/                 Asynq 消费端
│   │   ├── server.go           NewServer + NewRedisOpt
│   │   └── handler.go          RegisterHandlers (Deps 注入)
│   │
│   └── oapi/                   OpenAPI codegen 产物（DO NOT EDIT）
│       └── oapi.gen.go         ServerInterface + GetSpecJSON
│
└── pkg/                        通用工具（严禁 import internal/）
    ├── auth/jwt.go             JWTManager（Layer 1）
    ├── cache/                  Redis client 封装
    ├── database/               GORM 初始化 + 健康检查
    ├── errcode/                业务错误码集中地（type.go + common.go）
    ├── log/                    zap logger + trace_id ctx helper
    ├── response/response.go    统一响应 (code/message/reason/data/metadata)
    └── validator/              binding 错误翻译
```

加新模块时**对照这棵树**：handler / service / repository / model 各加一个文件，然后到 `internal/server.go` 装配，到 `internal/router/router.go` 注册路由。少一步都不算完成。
