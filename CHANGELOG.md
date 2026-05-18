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
