# go-skeleton 项目约定

> 给 Claude 看的项目专属规则。全局规则见 `~/.claude/CLAUDE.md`，这里只列本项目特有的约定。

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
| `internal/errcode/` | 业务错误码 | 只在这里定义新错误，不在 service/handler 内联构造 |
| `internal/task/` | Asynq 任务类型定义 | API 和 Worker 共享 |
| `internal/worker/` | Asynq 消费端的 handler 实现 | 复用 `service`/`repository`，不要重写业务逻辑 |
| `internal/taskqueue/` | Asynq client 的薄封装 | service 通过 `ExampleQueue` 这种接口依赖它，不直接 import asynq |
| `pkg/` | 跟业务无关的通用工具 | 严禁 import `internal/` 任何包 |

数据库迁移当前用 `cmd/migrate` 跑 GORM `AutoMigrate`，**没有** `migrations/` SQL 文件目录；如果未来引入 SQL 迁移工具，放仓库根目录的 `migrations/`，不要塞 `internal/`。

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
- 走事务时用 `repository.InTx(ctx, db, fn)` + `dbFromContext(ctx, r.db)`，让上层组合事务。

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

业务 API 固定返回 HTTP 200，靠 JSON body 的 `code` 区分。响应结构定义在 `pkg/response/response.go`：

```json
{ "code": 0, "msg": "success", "data": { ... } }
{ "code": 1001, "msg": "...", "reason": "INVALID_PARAMS", "metadata": { "trace_id": "..." } }
```

注意字段是 `msg`，不是 `message`（项目早期定下来的，前端契约不要动）。

例外：`/health` 用真实 HTTP 状态码（200 / 503），给 LB 和 K8s 探针用。

新增错误码：去 `internal/errcode/common.go` 加一个 `newError(code, "REASON")` 常量，并在 `pkg/response/response.go` 的 `messageFor` 里补默认英文文案。

## i18n

**当前未实现**。如果未来加，按下面这套约定来，不要散落到各层：

- 翻译文件统一放 `config/i18n/locales/{lang}.json`，key 用 `errcode` 的 `Reason`（如 `INVALID_PARAMS`）。
- 在 `pkg/response` 的 `messageFor` 内根据 `Accept-Language` 切换文案，handler 和 service **不**自己拼语言相关字符串。
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
- service 通过 `ExampleQueue` 接口依赖 `taskqueue.Queue`，不直接拿 `*asynq.Client`。
- Worker 消费端 handler 在 `internal/worker/handler.go` 注册，业务流程委托给 service。

## context 传递（硬约束）

请求级 `context.Context` 从 handler 一路传到 repository，**业务层禁止用 `context.Background()` 替换**。它带着 `trace_id`、超时、取消信号，断了会导致：HTTP 已超时但 DB 查询还在傻跑。

写测试可以用 `context.Background()`，业务代码不行。

## 环境变量

- 三个进程各自有 `cmd/{api,worker,migrate}/.env`，根目录 `.env` 是共享 fallback。
- 加载优先级：真实环境变量 > 进程专属 `.env` > 根 `.env`。
- 新增配置项必须更新 `.env.example`。所有 `.env*`（除 `.env.example`）都在 `.gitignore`，禁止把真实凭证落到仓库。

## 审计日志

`middleware.TraceLogger` 默认开启，按 `AUDIT_LOG_EXCLUDE_PATHS` 排除路径（如 `/health`）。日志字段用 zap 结构化输出，敏感字段 (`Authorization`、`Cookie`、body 里的 `password` / `token`) 自动脱敏。**不要绕过中间件自己打日志带请求体。**

`trace_id` 从 `applog.TraceIDFrom(ctx)` 取；service / repository 打日志统一用 `applog.FromContext(ctx).Xxx(...)`，不要 `applog.L()` 全局 logger 打业务日志。

## pkg/ 边界

`pkg/{auth,cache,database,log,response,validator}` 是通用工具。改这些包之前确认：

- 不引入对 `internal/` 任何包的依赖（`pkg/response` 引用 `internal/errcode` 是历史例外，不要扩散；新加包不要这么做）。
- 接口稳定优先于功能堆叠，因为理论上可以被其他项目复用。

## 写代码时常犯的错（已知会触发返工）

- ❌ 在 handler 写业务规则 → ✅ 挪到 service。
- ❌ service 收 `*gin.Context` → ✅ 收 `context.Context`，需要的字段由 handler 传 primitive。
- ❌ repository 之外的层 import `gorm.io/gorm` → ✅ 通过 service 包里定义的接口隔离。
- ❌ 用 `fmt.Errorf("xxx")` 直接返 → ✅ 返 `errcode.XxxError`；底层错误 `applog.FromContext(ctx).Error(..., zap.Error(err))` 记进日志。
- ❌ 响应字段写 `message` → ✅ 是 `msg`。
- ❌ 在 service 里 `context.Background()` 起新 ctx 调 DB → ✅ 传原 ctx；只有 fire-and-forget 后台任务才允许，且必须独立带超时。
- ❌ 给 Worker 复制一份和 API 不同的业务逻辑 → ✅ 共享 service。

## API 契约：OpenAPI 3.1

**真相源是 `api/openapi.yaml`**。改接口的标准流程：

1. 改 `api/openapi.yaml`。
2. `make oapi` 重新生成 `internal/oapi/oapi.gen.go`。
3. 改 `internal/handler/*` 让代码满足生成的 `oapi.ServerInterface`。
   编译期保险线是 `internal/handler/openapi.go` 里的：
   ```go
   var _ oapi.ServerInterface = (*APIServer)(nil)
   ```
   yaml 和代码一旦漂移，**build 直接失败**，不依赖人去 review 注释。
4. 改 `internal/router/router.go` 注册新路由（或调整中间件）。
5. `make verify` 通过——`oapi-verify` 会用 `git diff --quiet` 检查生成产物已 commit。

### 单一真相约定（重要）

oapi-codegen 会生成两类东西，**区分对待**：

- **协议层 schema**（`oapi.HealthResponse`、`oapi.HealthResponseChecks`、`oapi.ListExamplesParams`、`oapi.BearerAuthScopes` 等）—— handler **应该**直接用，让响应/参数结构跟 yaml 强对齐。
- **业务实体**（`oapi.Example`、`oapi.CreateExampleReq` 等业务请求/响应模型）—— handler / service / repository **不要** import；业务结构以 `internal/service` 包为准（如 `service.CreateExampleReq`、`service.ExampleService` 返回的 `*model.Example`）。

判断方法：如果生成的类型只服务于 transport 层（响应外壳、query params、security scope），用它；如果它承载业务字段（id/name/created_at 这种领域属性），不用它。

`internal/oapi` 包对外的关键导出：
- `oapi.ServerInterface`：用于编译期契约保险。
- `oapi.GetSpecJSON()`：用于 `/openapi.json` 返回 embedded spec。
- 协议层 schema 类型：handler 可以直接用。

`internal/oapi/oapi.gen.go` 顶部标了 `DO NOT EDIT`——**不要手改它**，改 yaml 然后 `make oapi`。它会入库（和 `go.sum` 一样），CI / 队友不需要重跑生成。

### 不做的事

- 不生成 client SDK（内部联调用不到）。
- 不生成 strict-server wrapper（会绕开 Gin 上下文，破坏现有 middleware）。
- 不引入 `swaggo/swag`、`gin-swagger`、`swagger-ui` 等运行时 UI 框架——前端用 `/openapi.json` 导入 Postman / Bruno / Insomnia 即可。
- 不用 `oapi.RegisterHandlers` 接管路由注册——业务路由仍走 `internal/router`，享受细粒度中间件控制（如 `/auth/me` 要 BearerAuth、`/auth/token` 不要）。

### oapi-codegen 对 OpenAPI 3.1 的支持

oapi-codegen 当前对 3.1 标注 "partial support"，跑生成会打 WARNING。本项目实测可用；如果未来某个 3.1 特性导致生成失败，**先看 yaml 能不能用 3.0 兼容写法表达**，不要回退到 3.0。

## 验证命令

声明任务完成前必须跑过：

```sh
go test ./...
go vet ./...
golangci-lint run
```

详见根目录 `README.md` 的"Verify"小节。

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
go test ./... -race                   # 带 race（CI 没开自动，写并发代码时手动跑）
go test ./... -cover                  # 看覆盖率
```

**不要给跑不通的测试加 `t.Skip()` 蒙混过关。**测试坏了就修，或者删——不要假装绿。

## Git Workflow

仓库已初始化，主干 `master`，远程 `origin` = `https://github.com/myxiaoao/go-skeleton`。

### Commit message

沿用全局 `~/.claude/CLAUDE.md` 的规则（type(scope): description + 详细变更说明）。本项目的 scope 约定：

| Scope | 对应改动 |
| --- | --- |
| `api` | `cmd/api/`、`internal/server.go`、HTTP 路由 / middleware、`api/openapi.yaml` 契约 |
| `worker` | `cmd/worker/`、`internal/worker.go`、`internal/worker/`、Asynq handler |
| `migrate` | `cmd/migrate/`、迁移相关 |
| `bootstrap` | `internal/bootstrap/`、`config/` |
| `handler` / `service` / `repository` / `model` / `router` / `middleware` / `errcode` / `task` / `taskqueue` | 对应 `internal/*` 子包 |
| `auth` / `cache` / `database` / `log` / `response` / `validator` | 对应 `pkg/*` 子包 |
| `oapi` | OpenAPI codegen 配置 / 生成产物 (`api/oapi-codegen.yaml`、`internal/oapi/`) |
| `build` | `Makefile`、构建脚本 |
| `ci` | `.github/workflows/*`、`.github/dependabot.yml` |
| `docs` | README、CLAUDE.md、注释 |
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
make verify   # fmt + vet + test + lint + oapi-verify
```

任意一项挂了**不要 `--no-verify` 跳过**——按全局规则，hook 失败先修问题再重新 commit，不要 amend。

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
├── Makefile                    开发与提交前一站式入口（make help 查全部 target）
├── README.md
├── CLAUDE.md                   本文件
├── go.mod / go.sum             模块名 go-skeleton
│
├── api/                        API 契约层
│   ├── openapi.yaml            真相源：OpenAPI 3.1 spec
│   └── oapi-codegen.yaml       codegen 配置
│
├── cmd/                        三个进程入口，main.go 只做启停
│   ├── api/main.go             HTTP 服务
│   ├── worker/main.go          Asynq 消费者
│   └── migrate/main.go         GORM AutoMigrate
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
│   │   ├── health.go           /health（用真实 HTTP 状态码）
│   │   └── openapi.go          /openapi.json + APIServer (满足 oapi.ServerInterface)
│   │
│   ├── service/                业务逻辑层（context.Context 入参）
│   │   └── example.go
│   │
│   ├── repository/             数据访问层（唯一允许写 GORM）
│   │   ├── example.go
│   │   └── tx.go               WithTx / InTx / dbFromContext
│   │
│   ├── model/                  GORM 数据结构
│   │   └── example.go
│   │
│   ├── middleware/             Gin 中间件
│   │   ├── auth.go             BearerAuth + AuthSubject
│   │   ├── cors.go
│   │   ├── logger.go           TraceLogger（审计日志 + 脱敏）
│   │   ├── rate_limit.go       IPRateLimiter
│   │   ├── recovery.go
│   │   └── timeout.go
│   │
│   ├── errcode/                业务错误码集中地
│   │   ├── type.go             Error 类型
│   │   └── common.go           InvalidParams / Unauthorized / ...
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
    ├── log/                    zap logger + trace_id ctx helper
    ├── response/response.go    统一响应 (code/msg/reason/data/metadata)
    └── validator/              binding 错误翻译
```

加新模块时**对照这棵树**：handler / service / repository / model 各加一个文件，然后到 `internal/server.go` 装配，到 `internal/router/router.go` 注册路由。少一步都不算完成。
