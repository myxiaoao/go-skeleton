# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once it leaves the unreleased phase.

Commit prefixes follow the convention in `CLAUDE.md`
(`type(scope): description`); group entries below by `Added`, `Changed`,
`Fixed`, `Removed`, `Security`, or `Deprecated`.

## [Unreleased]

### Fixed

- **new-endpoint 修审计发现的三个 hard stop**:
  上一版"yaml 反向驱动"承诺生成后立即 `make verify` 绿，实测发现三个漏点：
  (1) `internal/router/router_test.go::buildEngine` 的 deps fixture 不会被
  注入，新 spec 路径走 `TestRouterCoversAllSpecOperations` 时 404；
  (2) `registerXxxRoutes` 用写死的 `/<lower>s`，导致 `/api/v1/order-items`
  注册成 `/orderitemss`，违背"yaml 真相源"；
  (3) `Get`/`Update`/`Delete` 模板写死 `c.Param("id")` 与 service 参数
  名 `id`，yaml 用 `{order_id}` 时 gin 路径变 `/:order_id` 但 handler 取
  空字符串。
  本轮：`router_test.go::buildEngine` 加 `// NEH test-deps` 锚点；脚本注入
  zero-value handler；`collectOperations` 返回 yaml 真实 resourcePrefix
  做 `r.Group` 路径；`operation` 新增 `PathParamNames []string` 从 yaml
  path 正则提取，handler / service 模板用真实参数名；≥2 个 path 参数
  fail-fast 提示用 `x-handler-method` 覆盖手写。
  `scripts/scripts_test.go` 新增 5 个回归覆盖：路径来源、参数名、
  router_test 注入、router_test 缺失场景、多参数 fail-fast。

### Added

- **`make new-endpoint` 改 yaml 反向驱动**:
  原"复刻 Example 模板"换成读 `api/openapi.yaml` + `internal/oapi/oapi.gen.go`
  反向生成。脚本扫 `operationId` 含 NAME 的 operation，按 yaml `path` / `verb`
  推路由注册，按 `ServerInterface` 方法签名生成 `APIServer` 转发方法；
  handler 按动作模板（List/Create/Get/Update/Delete/EnqueueTask + 兜底）
  生成；service / repository 返 `errcode.NotImplementedYet` 占位让骨架直接
  `make verify` 通过。yaml extension `x-handler-method: <Action>` 可显式
  指定 handler 方法名（动作推不出来时）；yaml `security: [{ bearerAuth: [] }]`
  自动把对应路由放进 `deps.AuthRequired` 子组。
  新加 `errcode.NotImplementedYet`（9005）+ MessageFor；
  `internal/handler/openapi.go` 加 `// NEH apiserver-fields` /
  `apiserver-methods` 锚点。
  `scripts_test.go` 改用 `buildNewEndpoint`（先 `go build` 出 binary 再在 tmp
  workdir 跑）解决 kin-openapi 第三方依赖在 `t.TempDir()` 里没 go.mod 的问题；
  覆盖：注入完整、yaml 缺资源 fail-fast、重名拒绝、小写 NAME 拒绝、
  `x-handler-method` 覆盖、`bearerAuth` 路由分组。
  `CLAUDE.md` / `AGENTS.md` §API 契约 / `docs/development.md` §三 /
  `docs/runbook.md` §新增 endpoint 同步重写。

- **Release 供应链增强（SBOM + cosign keyless 签名）**:
  `.github/workflows/release.yml` 在 tag push 流程里加：(1) `anchore/sbom-action`
  生成 `sbom.spdx.json`（SPDX 2.3，扫源码 + go.mod，满足 EU CRA / 合规
  审计场景），(2) `sigstore/cosign-installer` 装 cosign v2.4.1，对 tarball
  / `SHA256SUMS` / SBOM 都跑 `cosign sign-blob`（keyless，私钥不落盘——
  走 GitHub OIDC + Fulcio + Rekor 透明日志），产物 `.sig` + `.crt` 与
  原 tarball 一起挂到 Release。`permissions` 加 `id-token: write` 让
  workflow 拿到 OIDC token。`docs/deploy.md` §1a 加 `cosign verify-blob`
  + Fulcio identity 正则的验签示例，以及 SBOM 用 grype 扫漏洞的方法。
- **CI 加 K8s manifest 校验**:
  `.github/workflows/ci.yml` 新加 `validate-k8s-manifests` job（与
  `validate-systemd-units` 平级）：先 `kubectl kustomize` 把 base +
  `overlays/production` 都构出来（patch target / strategic merge 漂了
  立即红），再用 kubeconform v0.6.7（pin 版本）`-strict` 做 schema
  校验——字段拼写错误 / 类型不符 / required 缺失都会被抓；ServiceMonitor
  这种 CRD 从 datreeio/CRDs-catalog 拉 schema，没找到时
  `-ignore-missing-schemas` 不阻塞。本地负向测试确认 `replicas` 拼成
  `replicass` 能被 kubeconform 捕获。
- **`scripts/*.go` 黑盒回归**:
  `scripts/scripts_test.go` 给 env-verify / architecture-verify /
  new-endpoint 加最小回归。脚本本身是 `//go:build ignore` main，
  没法直接 import；测试用 `t.TempDir` + `git init` 准备假仓库，
  用 `go run /abs/path/scripts/X.go` 走真实 CLI 入口。覆盖：env-verify
  happy path / 缺 .env.example 失败 / 注释字面量不误命中；architecture
  规则 1（gin 入 service）与规则 2（gorm 入 handler，repository 不被
  告警）；new-endpoint 注入完整、重名拒覆盖、小写 NAME 被拒。字段对齐
  场景用 `containsTokens` 容忍 gofmt 多空格，避免脆性断言。
- **K8s / Kustomize 部署模板**:
  `deploy/k8s/` 新增可直接 apply 的 Kustomize 基线：base/ 含 Namespace +
  ConfigMap（非敏感 env，与 .env.example 对齐）+ Secret 占位 schema
  （`secret.example.yaml`，不入库真值）+ API/Worker Deployment + Service
  （含独立 9090 metrics 端口）+ migrate Job + API HPA + ServiceMonitor，
  overlays/production/ 用 patches 覆盖镜像 tag / 副本数 / HPA 边界。
  liveness 探针打 /livez、readiness 打 /health（与 README 上线前检查
  清单对齐，避免 DB 抖动杀健康 Pod）；ServiceAccount 默认非 root、只读
  rootfs、drop ALL capabilities，与 systemd unit 的硬化思路一致。
  `kubectl kustomize deploy/k8s/overlays/production` 本地可构建（已测）。
  `deploy/k8s/README.md` 给完整部署 / 回滚 / 与 systemd 对应步骤；
  `docs/deploy.md` §10 接入指向。不引 Helm（一份 yaml 比 chart+values
  易读易 diff，团队按需自行包装）。
- **`make drop-example` 一键拔示例模块**:
  `scripts/drop-example.go`（//go:build ignore + go run，与
  `scripts/gen-errcodes.go` 同风格，不引第三方依赖）。删 12 个 Example
  分层文件 + 集成测试 + worker handler 测试 + 示例迁移；清 `server.go` /
  `router.go` / `worker.go` / `handler/openapi.go` 里的 Example 装配
  与转发方法（保留 `// NEH ...` 锚点）；清 `api/openapi.yaml` 的 paths
  / schemas / tags；保留 `db := reg.DB.DB()` 加 `_ = db` 静音占位
  （new-endpoint.sh 注入仍能拿到 db）；给 `dbFromContext` / `txFromContext`
  加静音哨兵；留 `migrations/00010101000001_placeholder.sql` 让
  `//go:embed *.sql` 不空。跑完调 `make oapi` + `go mod tidy` + verify
  子集（fmt/vet/test/lint/architecture/env/tidy/docs-verify）自检。安全
  网：拒绝 dirty checkout、每步 patch 幂等、找到多处文本视为模板漂移
  fail-fast。`docs/runbook.md` 加「移除骨架自带的 Example 示例模块」段。
- **`make dev-all` 一条命令起本地三进程**:
  `scripts/dev-all.sh` 串联 `dev-deps-check` → `cmd/migrate -cmd up` →
  并发起 API + Worker，stdout 加 `[api]` / `[worker]` 前缀；bash 3.2
  兼容（macOS 自带），Ctrl-C 优雅停（SIGTERM →
  `GRACEFUL_SHUTDOWN_TIMEOUT` 默认 15s → SIGKILL 兜底）。任一子进程
  退出会带走另一个，exit code 透传给 make。`docs/runbook.md` 同步
  「本地起完整三进程」段。
- **`make oapi-breaking` + CI 检测 OpenAPI 破坏性变更**:
  `scripts/oapi-breaking.sh` 用 [oasdiff](https://github.com/oasdiff/oasdiff)
  pin 版本 v1.16.0，对比 `OAPI_BREAKING_BASE_REF`（默认 origin/master）
  与工作树的 `api/openapi.yaml`，`--fail-on ERR` 只在确凿 breaking 时
  退出非零；空 base ref 给 fetch-depth 提示；同 ref 自比直接放过。
  `.github/workflows/ci.yml` 新增 oapi-breaking job，仅 PR 上跑，
  fetch-depth 0，base_ref 通过 env 传 + 字符白名单校验避免 GitHub
  Actions expression injection；故意 expand-contract 时用
  `OAPI_ALLOW_BREAKING=1` 跳过并在 PR 描述写明缘由。**不接入 `make
  verify`**：定位是 PR / 发版前门禁，本地不一定有 fresh origin/master，
  expand-contract 阶段会故意 breaking。
- **`new-endpoint.sh` 测试模板升级成可直接跑通的 smoke 用例**:
  handler / service / repository 各生成
  `Test${NAME}HandlerCreateSuccess` / `Test${NAME}ServiceCreateSuccess` /
  `Test${NAME}RepositoryCreate`，用 `mock${NAME}Repo` /
  `setup${NAME}Router` / `${LOWER}DryRunDB` 等带前缀的 helper 避开
  `example_test.go` 同包重名；生成后 `go test` 可立即通过，替代原
  `TestPlaceholder Skip`。假设新模块沿用 example 接口形态（Create /
  List / EnqueueTask）；改了业务签名后按 `example_test.go` 风格手补。
- **迁移文件 lint（migrations/migrations_test.go 扩充）**:
  在原有"版本号严格递增 + +goose Up/Down 注解"基础上新增两道门：
  (1) `TestMigrationsFilenameFormat` 强制
  `<14位 UTC 时间戳>_<snake_case>.sql`；
  (2) `TestMigrationsBreakingDDLMustBeMarked` 检测 Up 段里的
  `DROP TABLE/COLUMN/CONSTRAINT`、`ALTER COLUMN TYPE/SET NOT NULL`、
  `RENAME COLUMN/TO`、`TRUNCATE` 等破坏性 DDL，必须配
  `-- breaking: <reason>`（或 `-- +breaking <reason>`）显式标注，
  把 expand-contract 决策从"review 拍脑袋"提升成"commit 时必须自证"。
  另加 `TestMigrationsBreakingDDLDetectionLogic` 走表驱动反向用例
  确保正则真能拦下来，避免现仓库没破坏性 DDL 时 lint 沉默失败。
  CLAUDE.md / AGENTS.md 顶层目录段同步说明。
- **`/docs` 在线 API 文档（Stoplight Elements）**:
  `internal/handler/openapi.go` 新增 `OpenAPIHandler.Docs`，返回内嵌的
  Stoplight Elements 页面（unpkg CDN，锁版本 `@stoplight/elements@8.4.2`），
  通过 `apiDescriptionUrl="/openapi.json"` 复用现有 spec 端点渲染在线文档。
  挂在根级 `/docs`（与 `/openapi.json`、`/metrics` 同级），不进
  `oapi.ServerInterface`、不改 `api/openapi.yaml`。内嵌 fetch 拦截器从
  `localStorage` 的 `go_skeleton_token` 读 token，给 TryIt 请求自动加
  `Authorization` 头。**推翻**原先"不引入运行时 UI 框架"的约束：该约束针对
  把 UI 框架编译进 Go（swaggo/swag、gin-swagger）的重型方案，而 Stoplight
  Elements 是纯 CDN web component，零 Go 依赖、不接管路由，不带来这些代价；
  CLAUDE.md / AGENTS.md 已同步修订。依赖外网 CDN，内网/离线环境无法渲染。
  页面外观由启动期 `DOCS_*` env 配置（`DOCS_TITLE` / `DOCS_THEME`
  light·dark·system / `DOCS_LAYOUT` sidebar·stacked / `DOCS_HIDE_TRY_IT` /
  `DOCS_HIDE_SCHEMAS` / `DOCS_LOGO`），在 `NewOpenAPIHandler` 里一次性预渲染、
  运行时不变；`DOCS_THEME=system` 跟随系统 `prefers-color-scheme` 切换明/暗，
  并修复 Elements dark 模式代码高亮配色。`/docs` 与 `/openapi.json` 只在非
  生产环境注册（`newEngine` 里 `!Env.IsProduction()` 守卫）——`APP_ENV=production`
  时两条路由都不存在、访问得 404，隐藏 API 契约与文档 UI 减少信息泄露面。
- **Security response headers middleware**:
  `internal/middleware/security_headers.go` writes `X-Content-Type-Options:
  nosniff`, `X-Frame-Options: DENY`, and `Referrer-Policy: no-referrer` on
  every response. New env `SECURITY_HEADERS_ENABLED` (default `true`).
  HSTS is intentionally omitted (TLS terminates at the LB / reverse proxy,
  not the app); CSP is omitted because the JSON API does not render HTML.
- **Request body / header size limits**:
  `internal/middleware/body_limit.go` wraps `http.MaxBytesReader` around
  `c.Request.Body`; `internal/server.go` sets
  `http.Server.MaxHeaderBytes = 1MB`. New env `BODY_MAX_BYTES`
  (default `1048576`, `0` disables). Mitigates trivial OOM via a 100MB
  JSON or oversized headers; the body cap surfaces through the normal
  `INVALID_PARAMS` envelope.
- **`make tidy-verify`**: backs up `go.mod` / `go.sum`, runs
  `go mod tidy`, then diffs; fails when they drift from a fresh `tidy`.
  Wired into the `make verify` chain so CI catches manual `go get`
  changes that leave `go.sum` dirty.
- **Dependabot `docker` ecosystem**: `.github/dependabot.yml` adds a
  weekly scan of `FROM` lines in the `Dockerfile` so base-image CVEs get
  a PR instead of going unnoticed alongside the existing `gomod` /
  `github-actions` scans.
- **`APP_ENV` production safety guard**: new env `APP_ENV`
  (`development` | `production`, default `development`). When
  `production`, `config/validate.go::validateProductionSecrets` rejects a
  placeholder / empty / sub-32-byte `JWT_SECRET` and a `true`
  `AUTH_DEV_TOKEN_ENABLED` (fail-fast at startup); `cmd/api/main.go` warns
  on `RATE_LIMIT_PER_MINUTE=0`. Stops "copy `.env.example` to prod" from
  shipping a public JWT secret. `APP_ENV` is distinct from `GIN_MODE`.
- **Asynq Redis pool size is now tunable**:
  `bootstrap.AsynqRedisOpt` passes `REDIS_POOL_SIZE` through to
  `asynq.RedisClientOpt.PoolSize` (API client/inspector + worker server).
  Previously only the cache (go-redis) client honored it; the queue
  connections were stuck on the library default. `REDIS_MIN_IDLE_CONNS`
  stays cache-only (asynq does not expose it).
- **`scripts/new-endpoint.sh` 增强**：以前只复制 5 个分层文件 + 让人手动
  改 OpenAPI / server.go / router.go——3 步漏 1 步仓库就编译不过、AI 经常
  漏。现在增强成：
  - 复制 handler / service / repository / model / task 5 层 example.go
    并 sed 替换 Example→<Name>（不变）
  - 生成 handler / service / repository 三层最小测试 stub（init() 静音日
    志 + 占位 TestPlaceholder），让 `go test ./...` 不报 "no test files"，
    新开发者按 example_test.go 风格手补真实用例（不直接复制 example_test
    是因为同包 helper 重名 + 跨包类型引用都会让纯 sed 失败）
  - 改 `internal/server.go`：在 4 个 `// NEH ...` 锚点注释处自动注入
    repo / service / handler 装配链 + HTTPHandlers 字段 / return literal
  - 改 `internal/router/router.go`：注入 Dependencies 字段 + RegisterRoutes
    调用，文件尾追加 `register<Name>Routes` 函数（默认列表 / 创建 /
    EnqueueTask 三条路由）
  - 注入用 awk + 全行匹配 `^[[:space:]]*// NEH <marker>$`（不是子串），
    避免 godoc 里提到锚点名时被误命中；BSD/GNU sed 的 `i\` 多行处理差
    异也回避掉
  - 注入完成跑 `gofmt` 兜底缩进；脚本结尾再次断言所有锚点仍在
  - 生成完不自动跑 `go build`：剩余 yaml + APIServer 接口实现仍要人工
    （业务字段不可知），脚本结尾打印 yaml stub + APIServer 字段 / 方法
    骨架给你贴。模板复制完后业务路由层**可以直接编译运行**（example.go
    们不引用 oapi 类型），APIServer 那条 oapi 契约链补完即 `make verify`
    全绿
  对应在 `internal/server.go::HTTPHandlers` / `newHTTPHandlers` 和
  `internal/router/router.go::Dependencies` / `RegisterRoutes` 加了 6 个
  `// NEH <name>` 锚点行；hand-edit 时不要动锚点。
- **`.env.example` 同步校验接入 `make verify`**：新增 `make env-verify` +
  `scripts/env-verify.sh`，提取 `config/` 里 `os.Getenv` / `getEnvOrDefault`
  / `boolEnv` / `intEnv` / `int64Env` / `durationEnv` / `environmentEnv` /
  `queueWeightsEnv` 这些 helper 第一参数的 env key 字面量，与 `.env.example`
  里 `KEY=...` 行做双向 diff——读了但模板没列 / 列了但代码不读都会 fail。
  失败时输出 key + 文件:行号，便于编辑器跳转。本次接入暴露了 `TRUSTED_PROXIES`
  在代码里被读但 `.env.example` 模板缺失的真实漏洞（注释里提了但没有
  `TRUSTED_PROXIES=` 行），已补回。
- **架构静态校验接入 `make verify`**：新增 `make architecture-verify`
  + `scripts/architecture-verify.sh`，把以前只写在 CLAUDE.md / AGENTS.md
  里的 import 边界从"靠 AI / 人记住"变成"机器拦截"。失败时输出违规文
  件 + 行号，CI / 提交前直接定位。当前覆盖四条规则：
  - service / repository / model / task / worker / taskqueue 禁止 import
    gin（service 也被 worker 消费，绑 transport 框架会让 worker 跑不通）
  - `gorm.io/gorm` 只允许出现在 repository / model / bootstrap / pkg/database
    持久化装配链上，其他层禁止 import
  - `pkg/` 反向依赖 `internal/` 会让 pkg 失去"理论可被其他项目复用"的属性，
    一律禁止
  - service / handler 运行时代码禁止 `context.Background()`——这两层一定
    有外部传入的 ctx（HTTP request / asynq task），用 Background 会丢
    trace_id / 超时；bootstrap / `server.go` 起后台 goroutine 是合法例外，
    不在本规则范围
  测试文件统一豁免（mock 持 stub gin/gorm 是合法）。新增规则时同步更新
  脚本顶部 header + CLAUDE.md / AGENTS.md 对应规则段。
- **Asynq 幂等约定固化成 helper**：`internal/task/header.go` 提供一套统一
  约定，取代以前每个 NewXxxTask 工厂自己 hard-code MaxRetry / 单独写
  payload 字段的散乱写法。
  - `task.Header{Version, TraceID}` 嵌入所有 payload struct 顶部，JSON
    序列化字段被提升到顶层（兼容已有的 `TraceIDFromPayload` 顶层解析）。
    `task.NewHeader(traceID)` 构造时自动填 `PayloadSchemaVersion=1`。
  - `task.CheckHeader(h, supported)` 让 worker handler 反序列化后第一时间
    校验 schema version；超界返 `ErrUnsupportedPayloadVersion` 走 retry，
    不静默吞——吞了等于丢消息，走 retry 至少能从 archived 队列告警看见。
    `CurrentSupported = {Min: 1, Max: 1}` 配合 `PayloadSchemaVersion` 单
    测防漂移（升版本忘改 CurrentSupported 会立刻 fail）。
  - `task.DefaultOptions()` 返回 `MaxRetry=5 + Timeout=30s` 的 Option 切
    片；业务有长任务 / 特殊重试需求 append 覆盖。
  - `task.BuildTaskID(namespace, keys...)` 拼接稳定业务键给 `asynq.TaskID`
    用做永久全局去重；带 `MaxTaskIDLength=1KB` 的可读前缀 + SHA-256 后缀
    保护，避免 Redis key 过长，也避免简单截断造成误去重。
    短窗口防抖仍用 `asynq.Unique(ttl)`——两种语义不同的去重并存，让 caller
    显式选，不强行合一。
  - `task.MarshalPayload(taskType, payload)` 薄包装让序列化 error 自动带
    task type 上下文，便于线上日志定位。
  - `internal/task/example.go` 改造为新约定的参考实现：`ExamplePayload`
    嵌入 `Header`、`NewExampleTask` 用 `DefaultOptions()`；`internal/worker/handler.go::HandleExampleTask`
    加 `CheckHeader` 校验。CLAUDE.md / AGENTS.md "异步队列" shared 段
    + docs/runbook.md "新增任务" 与 "幂等约定" 段同步更新。
- **`repository.InTxWithOptions(ctx, db, *sql.TxOptions, fn)`**: 新增事务
  helper，允许调用方指定 isolation level（如 `sql.LevelRepeatableRead`、
  `sql.LevelSerializable`）和 `ReadOnly`。`InTx` 现在是 `InTxWithOptions(nil)`
  的薄包装，行为不变。典型用例：`repository.List` 想要 `total` 与分页
  rows 走同一快照，可用 `InTxWithOptions(ctx, db, &sql.TxOptions{
  Isolation: sql.LevelRepeatableRead, ReadOnly: true}, fn)` 包住。嵌套
  调用（ctx 已携带活跃事务）opts 会被忽略——isolation 只能在最外层
  `BeginTx` 定，子事务改不动。
- **`/metrics` 支持独立 listener（`METRICS_ADDR`）**：新增环境变量
  `METRICS_ADDR`，空字符串（默认）维持现状（`/metrics` 挂在业务 engine
  同端口）；非空（如 `127.0.0.1:9090`）时业务 engine 不再注册 `/metrics`，
  由独立 `http.Server` 监听该地址。`Server.Run` 先绑 metrics 端口、绑不上
  直接 fail-fast（比业务跑半截才发现要省事）；`Shutdown` / `Close` 一并停。
  独立 server mux 只挂 `/metrics`，其他路径一律 404，避免被误当业务端口。
  生产推荐绑 loopback 或内网地址，让业务端口公网暴露不会顺带泄露 Prometheus
  指标——与业务在 L4 层就隔离。`config.ProductionWarnings` 在 production 下
  `METRICS_ADDR` 为空时打 warn 提醒。

### Changed

- **scripts/ 6 个 bash 脚本改 Go**: `architecture-verify.sh` /
  `env-verify.sh` / `new-endpoint.sh` / `docs-verify.sh` /
  `oapi-breaking.sh` / `mod-upgrade.sh` → 同名 `.go`，统一
  `//go:build ignore + go run` 约定（与 `scripts/gen-errcodes.go`、
  `scripts/drop-example.go` 同栈）。收益：
  - `architecture-verify` / `env-verify` 走 `go/ast`，不会把字符串字面量 /
    注释 / godoc 里的 `"gorm.io/gorm"` / env key 误命中（旧 grep 版偶尔会）
  - `docs-verify` 维护 ``` / ~~~ 代码块状态，代码块内 `## x` 不当 heading
  - `mod-upgrade` 去掉 jq 依赖，stdlib `encoding/json` 流式解析 NDJSON
  - `new-endpoint` 与 `drop-example` 形成"加 / 减"工具对，跨平台一致
    （不再依赖 BSD/GNU sed 与 awk 方言差异），注入后 `parser.ParseFile`
    校验语法
  - `oapi-breaking` 走 `*exec.ExitError.ExitCode()` 透传 oasdiff 退出码
  保留为 bash 的 3 个：`dev-all.sh`（trap 信号 + 子进程编排，shell
  表达力更强）、`deploy-doc-verify.sh`（70 行纯 grep）、`rename.sh`
  （一次性脚本跑完即删）。配套：内部代码注释从
  `./scripts/new-endpoint.sh <Name>` 改成 `make new-endpoint NAME=<Name>`
  （`internal/server.go` / `internal/router/router.go` / `docs/development.md`），
  `.github/workflows/ci.yml` 注释中 `oapi-breaking.sh` 改 `.go`。
- **数据库迁移从 GORM AutoMigrate 切到 goose 版本化 SQL**: 真相源从 Go struct
  改为仓库根 `migrations/*.sql`（goose 格式，经 `//go:embed` 打进二进制）。
  `cmd/migrate` 用 goose 的 **Provider API**（不是全局函数）跑迁移，支持
  `-cmd up`（默认）/ `down`（回滚一版）/ `status`（看状态）；Makefile 配套
  `run-migrate` / `migrate-down` / `migrate-status` / `migrate-create name=xxx`。
  迁移文件用时间戳前缀命名（goose 时间戳风格，`<YYYYMMDDHHMMSS>_<描述>.sql`），
  首版 `20260521000001_create_examples_table.sql` 用 `IF NOT EXISTS` 兼容已跑过
  AutoMigrate 的旧库。
  **运维加固**：Provider 绑死 `DialectPostgres`（本项目只支持 Postgres，不拉其他
  方言的注册与依赖），并配 Postgres session advisory lock
  （`lock.NewPostgresSessionLocker`）——多实例/多机同时跑 migrate 时只有一个持锁
  执行、其余阻塞等待（默认重试 5s × 60 = 最多 5min），把"migrate 仅一次"从靠
  人工纪律变成机制保证、杜绝并发 DDL 竞态。迁移结果以结构化返回经 zap 汇报，
  不依赖 goose 全局 stdout logger。
  **破坏性流程变更**：改表结构不再靠改 model 等自动同步，必须写迁移文件；
  `AutoMigrate` 已**移除**（不留兜底，避免两套真相源打架）。新增
  `migrations` 包静态校验测试（不连库，校验迁移可解析、版本递增无重复）。
  生产滚动升级的迁移时序与 schema 回滚（expand-contract / `pg_dump` 备份优先）
  见 `docs/deploy.md` §4–§5；Docker / K8s 路径下的命令载体（迁移当独立
  容器 / K8s Job / Helm hook 跑，而非塞进每个业务副本的 initContainer）见
  `docs/deploy.md` §10——运维原则与二进制路径一致，advisory lock 兜底多副本
  并发抢锁不竞态。
- **Docker build injects buildinfo**: `Dockerfile` adds `VERSION` /
  `COMMIT` / `BUILD_TIME` build-args wired into the same
  `-ldflags -X go-skeleton/pkg/buildinfo.*` as the Makefile; `make
  docker-build` passes them from git. Container `/health` and `-version`
  no longer fall back to `dev/none/unknown`, so prod triage / rollback can
  confirm the running revision.
- **`/health` draining response matches the OpenAPI contract**: while
  draining (post-SIGTERM), the handler returned an ad-hoc
  `{"status":"draining"}`. It now returns a spec-compliant
  `oapi.HealthResponse` (`status: unhealthy` + `checks` + `build`) with
  503, so clients parsing the documented schema do not break. Semantics
  are unchanged (503 → LB removes the pod).
- **systemd `READY=1` is no longer sent optimistically**:
  `sdnotify.Watchdog` previously fired `READY=1` the moment it started —
  before the HTTP port was bound. `Server.Run` / `Worker.Run` now take an
  `onReady` callback; the API calls `sdnotify.Ready` only after
  `net.Listen` succeeds (port bound, about to serve). The worker now calls
  `asynq.Server.Start` (synchronous, returns startup error) instead of
  `Run`, fires `onReady` only after `Start` returns nil, then drives
  `Stop`/`Shutdown` off the caller's `ctx` — so a failed start (e.g. Redis
  unreachable) returns an error and never sends READY, and Asynq's built-in
  `waitForSignals` no longer competes with the main `signal.NotifyContext`.
  A bind / start failure no longer fools systemd into thinking startup
  succeeded.
- **`make test-integration` no longer reruns the unit suite**: the target
  was `go test -tags=integration ./...`, which compiled the integration
  files but also re-ran every unit test, making the integration job a
  superset and attributing unit-test flakiness to it. Now scoped with
  `-run Integration`; integration test functions must contain
  `Integration` in their name (documented in the Makefile).
- **pre-commit hook stops suggesting `--no-verify`**: the sensitive-file
  block previously told users to run `git commit --no-verify`, which
  contradicts the repo rule against bypassing hooks. It now guides toward
  unstaging the file or narrowing the deny rule / allowlist.

- **CORS `Access-Control-Allow-Credentials` is now opt-in**: previously the
  middleware always wrote `Allow-Credentials: true` for whitelisted origins.
  Stateless JWT APIs do not need it; if the whitelist ever shares Redis or
  cookies with another internal service, this was an unnecessary attack
  surface. New env `CORS_ALLOW_CREDENTIALS` (default `false`); set to `true`
  only when the browser needs to auto-attach cookie/session.
- **`service.NewExampleService` signature**: `queue ...ExampleQueue` (variadic)
  → `queue ExampleQueue` (explicit, `nil` means queue unavailable). The
  variadic form silently dropped a second queue argument; explicit makes
  the API unambiguous. Internal callers and tests updated.
- **`worker.Deps` no longer holds `*gorm.DB`**: the project rule "repository
  is the only layer allowed to import gorm" was being undermined by the
  worker package carrying a `DB` field for the example task. New
  `ExampleProcessor` interface in the worker package illustrates the
  correct path (worker → service → repository → gorm); `Deps.DB` is
  removed and `worker/handler.go` no longer imports `gorm.io/gorm`.
- **`worker.ExampleProcessor` 收紧成 typed contract**: 接口从空 `interface{}`
  改成有方法的 `ProcessExample(ctx, payload task.ExamplePayload) error`，
  worker handler 显式调"有名有姓"的方法，接错 service 时编译期就能发现。
  `HandleExampleTask` 删掉旧的"打日志兜底"弱类型路径，processor 报 error
  透传让 asynq 走 `MaxRetry`，避免静默吞任务。`internal/worker.go::buildWorkerDeps`
  在 `reg.DB` 可用时实际注入 `service.NewExampleService(...)`（之前只是示
  意注释）；`RegisterHandlers` 在 `Deps.Example` 为 nil 时回填新增的
  `noopExampleProcessor` 兜底（保留模板可运行性 + 打 warn 提醒接业务）。
  `ExampleService` 加 `ProcessExample` 方法实现接口。`docs/runbook.md`
  "新增 Asynq 异步任务"小节扩成 5 步清单，明确 typed processor 接口形态。
- **`APP_ENV=production` 安全 guard 扩充**: `config/validate.go` 在
  production 下新增硬拦 `GIN_MODE != release` / `LOG_FORMAT != json`
  ——这两项漏配会泄露 panic stack / 日志采集器解析不了，是 `.env.example`
  checklist 的高频漏项，拦在启动期最稳。同时抽出 `config.ProductionWarnings`
  集中输出"非致命但大概率漏配"提示（`RATE_LIMIT_PER_MINUTE=0` /
  `TRUSTED_PROXIES` 空 / `METRICS_ADDR` 空）。`cmd/api/main.go` 启动期一次
  性 iterate warnings 打 log，不再散落多处。

### Docs

- `internal/middleware/timeout.go`: godoc now warns the middleware is unsafe
  for streaming / SSE handlers (Gin's synchronous `c.Next()` + `Written()`
  check truncates the response without writing the error envelope). The
  current skeleton has no streaming endpoints, but the caveat documents the
  trap before someone falls into it.
- `internal/repository/example.go::List`: godoc notes that `Count` + `Find`
  are independent queries under `READ COMMITTED`, so `total` is an
  approximation. Callers needing strong consistency should wrap in
  `InTx` + `REPEATABLE READ`.

### Fixed

- **JWT issuer can be silently disabled**: `pkg/auth.JWTManager.ParseToken`
  skipped iss-claim validation when `Issuer` was empty, so any token signed
  with the same secret but a different iss would still pass. `config/validate.go`
  now rejects empty `JWT_ISSUER` whenever `JWT_SECRET` is set; the JWTManager
  godoc explicitly warns about the empty-issuer behaviour for `pkg/auth`
  reusers. The `.env.example` default `JWT_ISSUER=go-skeleton` keeps existing
  deployments green.
- **Worker retry delay overflow**: `internal/worker.computeRetryDelay`
  replaces inline `time.Duration(1<<uint(n)) * baseDelay`. The old expression
  overflowed `int64` at large `n` and returned a negative `time.Duration` that
  the `delay > maxDelay` guard could not catch (negative < positive). New
  function caps at `n >= 30`, normalises negative `n`, and treats any
  non-positive computed delay as "use max". Asynq's default max retries (25)
  never reached the bug in practice, but the guard is cheap and covers
  custom Retry tunings.
- **Worker had no systemd watchdog**: `cmd/worker/main.go` now starts
  `sdnotify.Watchdog(ctx, cfg.Server.WatchdogInterval)`; the worker unit
  switches to `Type=notify` + `NotifyAccess=main` + `WatchdogSec=60s`
  (wider than the API's 30s, since worker tasks can legitimately block on
  long DB / external I/O between heartbeats). Adds `LimitNOFILE=65535` to
  match the API unit. `docs/deploy.md` section 9 splits into API / Worker /
  Migrate subsections.
- **Flaky rate-limiter goroutine assertion**: `TestIPRateLimiterStop`
  used process-wide `runtime.NumGoroutine()` deltas to assert the cleanup
  goroutine started/stopped, which races with other concurrently-running
  tests (observed failure: `before=3 now=3`). `IPRateLimiter` now closes a
  `done` channel when `cleanupLoop` exits and `Stop` waits on it, so the
  test asserts deterministically that `Stop` returns instead of polling a
  global counter.

### Tests

- **`internal/middleware/rate_limit_test.go`** (was 0 coverage): burst /
  block, per-IP isolation, zero-budget disabling, `cleanup` staleness
  pruning, `Stop` releases the cleanup goroutine, idempotent `Stop`.
- **`internal/worker/server_test.go`** (was 0 coverage):
  `computeRetryDelay` table tests including the overflow boundary at
  `n=30,100`; invariant test `delay ∈ [0, max]` over `n ∈ [-5, 100]`;
  `taskLogContext` trace-source resolution (payload vs synthesised);
  `traceMiddleware` does not swallow handler errors.

### Security

- **JWT 强制要求 `exp` claim，闭环永不过期 token**: `pkg/auth.JWTManager.ParseToken`
  显式启用 `jwt.WithExpirationRequired()`——jwt/v5 默认 `exp` 是 optional，
  没有它即使拿到 secret 也能签出永不过期 token，与文档声明的 Layer 1
  "exp 校验" 约束不符。同时 `GenerateToken` 在 `manager.ttl <= 0` 时返
  回新增 `ErrMissingTTL`，避免自家签出无 exp token 自己验不过；
  `GenerateTokenWithClaims` 同步要求 `claims.ExpiresAt != nil`，堵住自定
  义 claims 旁路。

### Added

- **PR / Issue templates**: `.github/pull_request_template.md` and
  `.github/ISSUE_TEMPLATE/{bug,feature,config}.yml` with the project's
  hard rules baked into the checklists (no testify/Wire, msg→message,
  oapi sync, env example sync, …).
- **CODEOWNERS template** at `.github/CODEOWNERS` covering OpenAPI,
  deploy, CI, `pkg/`, bootstrap, and the AGENTS.md / CLAUDE.md rule files.
- **`make sec`** wires `govulncheck` + `gosec` (versions pinned in the
  Makefile). Decoupled from `make verify` to avoid CVE-database churn
  causing flaky local runs; `.github/workflows/security.yml` schedules a
  weekly scan and supports manual dispatch.
- **Integration test build tag**: `make test-integration` runs only
  `//go:build integration` files; `make test` and CI stay fast.
  `internal/repository/example_integration_test.go` is the template.
- **`make docs-deploy-check`** (and a verify-step) keeps `docs/deploy.md`
  in sync with `deploy/systemd/*.service` — paths, `EnvironmentFile`,
  `User=` / `Group=`, and referenced unit filenames cross-checked by
  `scripts/deploy-doc-verify.sh`.
- **`docs/errcodes.md`** generated from `pkg/errcode` + `pkg/response.MessageFor`
  via `scripts/gen-errcodes.go`. `make docs-errcodes` regenerates;
  `make docs-errcodes-verify` (run by `make verify`) fails when out of
  sync, so adding an errcode without docs trips CI.
- **`make watch`** runs the API with `air` hot reload (`.air.toml`).
  `air` is installed on first use, kept off the default `make init`
  path; `tmp/` ignored.
- **`docker compose --profile debug up -d asynqmon`** (and `make
  dev-asynqmon`) exposes the Asynq Web UI on `127.0.0.1:8980`. Off by
  default; the profile keeps it out of the regular `make dev-up`.
- **Pre-commit hook template** at `.githooks/pre-commit` + `make
  hooks-install`. Runs `make fmt vet`, blocks `.env` / `*.pem` /
  `credentials.json` from being staged, supports `FULL=1` opt-in for a
  full `make verify`.
- **Example teaching headers**: every `internal/{handler,service,
  repository,model,task}/example.go` now opens with a short package-doc
  comment explaining what that layer is allowed and forbidden to do, so
  new contributors and AI assistants can mirror the pattern.
- **`.env.example` self-documentation**: every variable now has a 1-3
  line comment explaining purpose, legal values, and the production
  default to aim for. `/livez` also added to `AUDIT_LOG_EXCLUDE_PATHS`
  alongside `/health`.
- `pkg/response.MessageFor` exported so `scripts/gen-errcodes.go` can
  reuse the same default-message table without forking it; `INTERNAL_ERROR`
  picks up its own message instead of falling through to "operation
  failed".
- **Startup dependency probe (fail-fast)**: `internal/bootstrap/{api,worker}.go::InitXxx`
  pings DB / Redis immediately after opening them (`STARTUP_PROBE_TIMEOUT=5s`);
  on failure it releases the resources and exits non-zero, so systemd
  restarts on misconfiguration instead of running degraded.
- **pprof debug endpoint**: `internal/router/pprof.go` + `PPROF_ENABLED=false /
  PPROF_ADDR=127.0.0.1:6060`. Separate mux and listener for network-layer
  isolation; off in production by default, accessed via SSH tunnel +
  `go tool pprof`. Runbook gained an "open pprof" troubleshooting section.
- **Graceful drain + `/health 503`**: `Registry.Draining *atomic.Bool` acts as
  the process-wide graceful signal; on SIGTERM `cmd/api/main.go` flips it
  and sleeps `GRACEFUL_DRAIN` (default 10s) so the LB can pull the pod out
  of rotation before `Shutdown`. `/livez` is unaffected.
- **`/health` tiered status (`degraded`)**: `HealthResponse.status` adds a
  `degraded` enum value (backwards-compatible). DB down → `unhealthy` + 503
  (LB drains); Redis down → `degraded` + 200 (LB keeps the pod, since cache
  flaps shouldn't take the pod offline).
- **Recovery middleware trace_id fallback**: `middleware/recovery.go` panic
  log explicitly adds `zap.String("trace_id", c.GetString(...))`, so the
  field is always present (possibly empty) even if middleware ordering is
  reshuffled and `applog.FromContext` can't pick it up from ctx.
- **`make new-endpoint NAME=Foo`**: `scripts/new-endpoint.sh` copies
  `internal/{handler,service,repository,model,task}/example.go` five times,
  `sed`-renames `Example` → `<Name>` only inside the new files, and prints
  the three manual wiring steps (openapi.yaml, server.go, router.go).
- **`config.Load()` validation pass**: `config/validate.go` centralises
  startup constraints — `RequestTimeout > 0`, `GracefulDrain >= 0`,
  `DB_MAX_OPEN_CONNS > 0` (when DSN is non-empty), `WORKER_CONCURRENCY > 0`
  (when Queues is non-empty), `RATE_LIMIT_PER_MINUTE >= 0`. Misconfiguration
  fails fast in `cmd/main` rather than at first business request.
- **`make mod-upgrade`**: `scripts/mod-upgrade.sh` parses `go list -m -u
  -json` via `jq`, classifies direct deps by semver MAJOR (major / v0.x
  bumps print only; patch / minor auto-applied via `go get` → `go mod tidy`
  → `make verify`; any failure triggers `git checkout -- go.mod go.sum`
  rollback).
- **systemd `Type=notify` + `WatchdogSec=30s` + `LimitNOFILE=65535`**: new
  `pkg/sdnotify` package — `linux` build tag does the real sd_notify,
  other platforms ship a noop stub. `cmd/api/main.go` runs a goroutine
  emitting `READY=1` and periodic `WATCHDOG=1`. Adds dep
  `github.com/coreos/go-systemd/v22`. Worker / migrate units unchanged.
- **CI `validate-systemd-units` job**: `.github/workflows/ci.yml` adds a
  dedicated job that runs `sudo systemd-analyze verify
  deploy/systemd/*.service` against placeholder targets (dummy binaries,
  env file, user) so unit-file syntax / `Type=notify` consistency errors
  fail on push.
- **Runbook P0 troubleshooting table**: `docs/runbook.md` gained an 11-row
  matrix mapping symptoms ("API won't start", "OOM restart", "watchdog
  restart", "FD exhaustion", "/health degraded vs 503", …) to the first
  command to run (journalctl / ss / Asynqmon / pg_stat_activity /
  `/proc/$pid/limits` etc.).
- **`.golangci.yml` explicit `errcheck`**: enabled explicitly (v2 has it
  on by default; the explicit declaration makes lint reports clearer);
  `settings.errcheck.exclude-functions` lists `gin.Context.Error` and
  `fmt.Fprint*` as intentionally ignored, to avoid encouraging meaningless
  `_ =` assignments.

### Changed

- `make verify` chain now also runs `docs-deploy-check` and
  `docs-errcodes-verify` so doc drift fails fast.

### Added

- `scripts/rename.sh` one-shot rename helper. Pass
  `NEW_MODULE NEW_SHORTNAME`; it rewrites Go imports, `go.mod`, Makefile
  vars, `.env.example`, `.golangci.yml`, OpenAPI title, systemd unit file
  names + contents, `docker-compose` container names, JWT issuer defaults,
  and test fixtures, then runs `fmt + vet + test + lint + docs-verify`
  to confirm the rewrite is clean. README / README_zh / runbook updated
  to point at it instead of the previous hand-rolled `sed` command.
- **Binary deployment path** alongside the Docker path. `make build-linux`
  cross-compiles static `linux/amd64` + `linux/arm64` binaries (CGO off,
  `-tags netgo`, `-trimpath`); `make release` packages them with the
  systemd units, `.env.example`, and `DEPLOY.md` into per-arch tarballs
  plus a `SHA256SUMS` manifest.
- `.github/workflows/release.yml` publishes those tarballs to GitHub
  Releases on every `v*` tag push.
- `deploy/systemd/{go-skeleton-api,go-skeleton-worker,go-skeleton-migrate}.service`
  unit templates with security hardening (NoNewPrivileges, ProtectSystem,
  PrivateTmp, etc.).
- `docs/deploy.md` step-by-step binary deployment guide: host setup,
  systemd install, rolling upgrade, rollback, journald queries, and a
  troubleshooting cheat sheet.
- `pkg/buildinfo` exposes `Version` / `Commit` / `BuildTime` injected via
  ldflags; each `cmd/` binary supports `-version`, `/livez` includes the
  version, and `/health` returns a `build` object so monitoring can
  scrape the running version without a separate endpoint.

### Breaking
- Response envelope field renamed from `msg` to `message` to drop the
  abbreviation. Update any client that reads `response.msg`. The Go field
  name (`Response.Message`) is unchanged; only the JSON tag and the
  OpenAPI schema move.

### Added
- `/livez` liveness probe; `/health` is now documented as the readiness probe.
- `cmd/worker` performs a two-phase shutdown (`Stop` then `Shutdown`) so
  in-flight Asynq tasks complete before exit.
- `make dev-up` / `make dev-down` spin up Postgres + Redis via docker-compose.
- Multi-stage `Dockerfile` and `make docker-build` / `make docker-run` for
  the API process; the same Dockerfile builds worker / migrate via
  `CMD_TARGET`.
- README "Production Checklist" and "Using this Skeleton" sections.
- `.golangci.yml` enables gofumpt + gci formatters; gci uses explicit
  three-section import grouping (`standard / default / prefix(go-skeleton)`)
  so the short module name doesn't get misread as stdlib. `make fmt` now
  routes through `golangci-lint fmt`. Tool versions pinned in the Makefile.
- `make verify` prints a coloured banner before each step and on
  success/failure, so AI assistants and humans can spot the failing step
  instantly.
- `make docs-verify` and `scripts/docs-verify.sh` keep the shared sections
  of `AGENTS.md` and `CLAUDE.md` in sync.
- `docs/runbook.md` cheat sheet of machine-executable commands (add
  endpoint, add task, run specific tests, troubleshoot); README /
  AGENTS.md / CLAUDE.md link to it.
- `.gitattributes` marks `internal/oapi/oapi.gen.go`, `*.gen.go`, and
  `go.sum` as generated so GitHub diff collapses them and language stats
  ignore them.
- AGENTS.md / CLAUDE.md gained an "AI 助手提示" section listing the
  high-frequency rules AI assistants tend to violate (don't edit
  `oapi.gen.go`, don't import `oapi.Example`, don't inject `*gin.Context`,
  etc.).

### Changed
- `make init` verifies installed tool versions against the pinned ones and
  reinstalls when they differ.
- CI now runs `make test-race` on top of `make verify`.

### Fixed
- `POST /api/v1/auth/token` stays registered when JWT is unconfigured and
  returns `SERVICE_DISABLED`, matching the OpenAPI contract instead of 404.
