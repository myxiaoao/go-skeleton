# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once it leaves the unreleased phase.

Commit prefixes follow the convention in `CLAUDE.md`
(`type(scope): description`); group entries below by `Added`, `Changed`,
`Fixed`, `Removed`, `Security`, or `Deprecated`.

## [Unreleased]

### Changed

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
