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

### Changed
- `make init` verifies installed tool versions against the pinned ones and
  reinstalls when they differ.
- CI now runs `make test-race` on top of `make verify`.

### Fixed
- `POST /api/v1/auth/token` stays registered when JWT is unconfigured and
  returns `SERVICE_DISABLED`, matching the OpenAPI contract instead of 404.
