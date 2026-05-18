# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once it leaves the unreleased phase.

Commit prefixes follow the convention in `CLAUDE.md`
(`type(scope): description`); group entries below by `Added`, `Changed`,
`Fixed`, `Removed`, `Security`, or `Deprecated`.

## [Unreleased]

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
