# Multi-stage build for go-skeleton.
# Default target is cmd/api. Build cmd/worker or cmd/migrate by overriding
# CMD_TARGET at build time, e.g.:
#   docker build --build-arg CMD_TARGET=worker -t go-skeleton-worker .

ARG GO_VERSION=1.26.3

FROM golang:${GO_VERSION}-bookworm AS builder

ARG CMD_TARGET=api

# Build metadata injected into pkg/buildinfo via -ldflags -X (mirrors the
# Makefile). The build context has no .git, so pass these from CI:
#   docker build \
#     --build-arg VERSION=$(git describe --tags --always --dirty) \
#     --build-arg COMMIT=$(git rev-parse --short HEAD) \
#     --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) .
# Without them the binary falls back to dev/none/unknown — fine locally, but
# unhelpful for prod triage / rollback, so wire them up in the release pipeline.
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown

WORKDIR /src

# Cache module downloads in a separate layer so source edits don't re-fetch.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Static build: CGO off + netgo so the binary runs in distroless static.
# -trimpath strips local paths from binaries (reproducible builds).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -tags netgo \
        -ldflags="-s -w \
            -X 'go-skeleton/pkg/buildinfo.Version=${VERSION}' \
            -X 'go-skeleton/pkg/buildinfo.Commit=${COMMIT}' \
            -X 'go-skeleton/pkg/buildinfo.BuildTime=${BUILD_TIME}'" \
        -o /out/app ./cmd/${CMD_TARGET}

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# Run as the built-in nonroot user (uid 65532) from the base image.
COPY --from=builder /out/app /app

EXPOSE 3000

ENTRYPOINT ["/app"]
