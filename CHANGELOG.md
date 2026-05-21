# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once it leaves the unreleased phase.

Commit prefixes follow the convention in `CLAUDE.md`
(`type(scope): description`); group entries below by `Added`, `Changed`,
`Fixed`, `Removed`, `Security`, or `Deprecated`.

## [Unreleased]

### Added

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

### Changed

- **数据库迁移从 GORM AutoMigrate 切到 goose 版本化 SQL**: 真相源从 Go struct
  改为仓库根 `migrations/*.sql`（goose 格式，经 `//go:embed` 打进二进制）。
  `cmd/migrate` 改用 goose 库 API，支持 `-cmd up`（默认）/ `down`（回滚一版）/
  `status`（看状态）；Makefile 配套 `run-migrate` / `migrate-down` /
  `migrate-status` / `migrate-create name=xxx`。迁移文件用时间戳前缀命名
  （goose 时间戳风格、对齐 Laravel，`<YYYYMMDDHHMMSS>_<描述>.sql`），首版
  `20260521000001_create_examples_table.sql` 用 `IF NOT EXISTS` 兼容已跑过
  AutoMigrate 的旧库。
  **破坏性流程变更**：改表结构不再靠改 model 等自动同步，必须写迁移文件；
  `AutoMigrate` 已**移除**（不留兜底，避免两套真相源打架）。新增
  `migrations` 包静态校验测试（不连库，校验迁移可解析、版本递增无重复）。
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
