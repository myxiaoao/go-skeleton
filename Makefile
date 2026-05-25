.DEFAULT_GOAL := help

# 项目元信息
MODULE      := go-skeleton
BIN_DIR     := bin
API_BIN     := $(BIN_DIR)/api
WORKER_BIN  := $(BIN_DIR)/worker
MIGRATE_BIN := $(BIN_DIR)/migrate

# 本地默认 API 端口（与 cmd/api/.env.example SERVER_PORT 对齐）
API_PORT ?= 3000

# go 命令统一开关
GO       ?= go
GOFLAGS  ?=

# 版本元数据：默认从 git 取，可被环境变量覆盖。VERSION 优先用最近的
# tag（git describe --tags --dirty），无 tag 时退回 0.0.0-<short-sha>。
# 注入到 pkg/buildinfo.{Version,Commit,BuildTime} 三个变量。
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-unknown")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# -s -w 缩小二进制；-X 把版本元数据注入 buildinfo 变量。
LDFLAGS  ?= -s -w \
	-X 'go-skeleton/pkg/buildinfo.Version=$(VERSION)' \
	-X 'go-skeleton/pkg/buildinfo.Commit=$(COMMIT)' \
	-X 'go-skeleton/pkg/buildinfo.BuildTime=$(BUILD_TIME)'

# 工具链版本固定。升级时改这里 + 跑 make init 重新装，让 CI / 队友复现一致。
GOLANGCI_LINT_VERSION ?= v2.12.2
OAPI_CODEGEN_VERSION  ?= v2.7.0
# 格式化工具：gofumpt 收紧 gofmt 风格细节（多余空行、struct 对齐等），
# gci 用显式 sections 控制 import 分组（standard / default / prefix），
# 避免短 module name（go-skeleton 不含 dot）被误判成 stdlib 的老坑。
GCI_VERSION           ?= v0.14.0
GOFUMPT_VERSION       ?= v0.10.0
# 安全扫描工具。govulncheck 查已公布的 CVE；gosec 静态扫描代码里的安全反模式
# （硬编码密钥、SQL 拼接、不安全的随机数等）。两者跑独立 target，不进 verify
# 默认链路——CVE 数据库更新会让 verify 变成 flaky；CI 单独跑 make sec。
GOVULNCHECK_VERSION   ?= v1.1.4
GOSEC_VERSION         ?= v2.22.0
# air：本地热重载。仅在 make watch 时按需安装，不进 init 默认链路，
# 避免新人 clone 后被强制装一个开发期可选工具。
AIR_VERSION           ?= v1.62.0

.PHONY: help
help: ## 列出所有可用 target
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
	     /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ---------- 开发依赖 ----------

.PHONY: init
init: ## 安装/对齐辅助工具到 pin 版本（已是 pin 版本则跳过）
	@$(MAKE) --no-print-directory _ensure-golangci-lint
	@$(MAKE) --no-print-directory _ensure-oapi-codegen
	@$(MAKE) --no-print-directory _ensure-gci
	@$(MAKE) --no-print-directory _ensure-gofumpt
	@echo "init done."

.PHONY: hooks-install
hooks-install: ## 把 .githooks/pre-commit 接入本地 git（每个 clone 各装一次）
	git config core.hooksPath .githooks
	@echo "hooks installed. 取消：git config --unset core.hooksPath"

# Compares the installed tool version against the pinned version and
# reinstalls when they differ. Keeps team / CI reproducible: a stale
# pre-existing binary no longer slips past `make init`.
.PHONY: _ensure-golangci-lint
_ensure-golangci-lint:
	@want="$(GOLANGCI_LINT_VERSION)"; want_short="$${want#v}"; \
	if command -v golangci-lint >/dev/null 2>&1; then \
		got=$$(golangci-lint version --short 2>/dev/null); \
		if [ "$$got" = "$$want_short" ]; then \
			echo "golangci-lint $$got: ok"; exit 0; \
		fi; \
		echo "golangci-lint $$got != $$want_short, reinstalling..."; \
	else \
		echo "Installing golangci-lint $$want..."; \
	fi; \
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$$want

.PHONY: _ensure-oapi-codegen
_ensure-oapi-codegen:
	@want="$(OAPI_CODEGEN_VERSION)"; \
	if command -v oapi-codegen >/dev/null 2>&1; then \
		got=$$(oapi-codegen --version 2>/dev/null | awk 'NR==2{print; exit}'); \
		if [ "$$got" = "$$want" ]; then \
			echo "oapi-codegen $$got: ok"; exit 0; \
		fi; \
		echo "oapi-codegen $$got != $$want, reinstalling..."; \
	else \
		echo "Installing oapi-codegen $$want..."; \
	fi; \
	$(GO) install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$$want

# gci --version 输出形如 "gci version 0.14.0"，提取第三个字段并补回 v 前缀做比较。
.PHONY: _ensure-gci
_ensure-gci:
	@want="$(GCI_VERSION)"; want_short="$${want#v}"; \
	if command -v gci >/dev/null 2>&1; then \
		got=$$(gci --version 2>/dev/null | awk '{print $$3; exit}'); \
		if [ "$$got" = "$$want_short" ]; then \
			echo "gci v$$got: ok"; exit 0; \
		fi; \
		echo "gci v$$got != $$want_short, reinstalling..."; \
	else \
		echo "Installing gci $$want..."; \
	fi; \
	$(GO) install github.com/daixiang0/gci@$$want

# gofumpt -version 输出形如 "v0.10.0 (go1.x)"，取第一个字段。
.PHONY: _ensure-gofumpt
_ensure-gofumpt:
	@want="$(GOFUMPT_VERSION)"; \
	if command -v gofumpt >/dev/null 2>&1; then \
		got=$$(gofumpt -version 2>/dev/null | awk '{print $$1; exit}'); \
		if [ "$$got" = "$$want" ]; then \
			echo "gofumpt $$got: ok"; exit 0; \
		fi; \
		echo "gofumpt $$got != $$want, reinstalling..."; \
	else \
		echo "Installing gofumpt $$want..."; \
	fi; \
	$(GO) install mvdan.cc/gofumpt@$$want

# ---------- 本地开发依赖（docker compose） ----------

COMPOSE ?= docker compose

.PHONY: dev-up
dev-up: ## 启动本地 Postgres + Redis（后台运行；端口对齐 .env.example）
	$(COMPOSE) up -d
	@echo "postgres -> 127.0.0.1:5432 (user/password, db=app)"
	@echo "redis    -> 127.0.0.1:6379"

.PHONY: dev-down
dev-down: ## 停止本地依赖容器；数据卷保留
	$(COMPOSE) down

.PHONY: dev-reset
dev-reset: ## 停止并销毁数据卷（破坏性：DB / Redis 数据会丢）
	$(COMPOSE) down -v

.PHONY: dev-logs
dev-logs: ## 跟随本地依赖容器日志
	$(COMPOSE) logs -f

.PHONY: dev-asynqmon
dev-asynqmon: ## 启动 Asynqmon Web UI（http://127.0.0.1:8980；profile=debug）
	$(COMPOSE) --profile debug up -d asynqmon
	@echo "asynqmon -> http://127.0.0.1:8980 (生产请勿暴露此端口)"

# dev-deps-check 用宿主进程 / docker 都适用：只看 .env 里的 POSTGRES /
# REDIS_ADDR 能不能连。不用 docker 路径的用户跑 brew / apt 装完依赖后用
# 它做一次性 sanity check；docker 路径用户也可以用它确认 dev-up 后端口
# 通了。失败给出对应平台的安装提示，避免 "为什么连不上" 这种问题。
#
# SHELL 显式指 bash：探活靠 bash 内建 /dev/tcp，POSIX sh / dash 不支持。
.PHONY: dev-deps-check
dev-deps-check: SHELL := /usr/bin/env bash
dev-deps-check: ## 探活本机 Postgres + Redis（不用 docker 装完依赖后跑它）
	@set -e; \
	addr_pg=$$(grep -E '^POSTGRES=' .env 2>/dev/null | sed -E 's/^POSTGRES=//'); \
	addr_redis=$$(grep -E '^REDIS_ADDR=' .env 2>/dev/null | sed -E 's/^REDIS_ADDR=//'); \
	addr_pg=$${addr_pg:-postgres://user:password@127.0.0.1:5432/app?sslmode=disable}; \
	addr_redis=$${addr_redis:-127.0.0.1:6379}; \
	pg_host=$$(echo "$$addr_pg" | sed -E 's|.*@([^:/]+).*|\1|'); \
	pg_port=$$(echo "$$addr_pg" | sed -nE 's|.*@[^:]+:([0-9]+).*|\1|p'); \
	pg_port=$${pg_port:-5432}; \
	redis_host=$$(echo "$$addr_redis" | cut -d: -f1); \
	redis_port=$$(echo "$$addr_redis" | cut -d: -f2); \
	redis_port=$${redis_port:-6379}; \
	printf 'checking postgres %s:%s ... ' "$$pg_host" "$$pg_port"; \
	if (echo > /dev/tcp/$$pg_host/$$pg_port) 2>/dev/null; then echo OK; else \
		echo FAIL; echo "  brew:  brew install postgresql@17 && brew services start postgresql@17"; \
		echo "  apt:   sudo apt install -y postgresql && sudo systemctl enable --now postgresql"; \
		echo "  docker: make dev-up"; exit 1; fi; \
	printf 'checking redis    %s:%s ... ' "$$redis_host" "$$redis_port"; \
	if (echo > /dev/tcp/$$redis_host/$$redis_port) 2>/dev/null; then echo OK; else \
		echo FAIL; echo "  brew:  brew install redis && brew services start redis"; \
		echo "  apt:   sudo apt install -y redis-server && sudo systemctl enable --now redis-server"; \
		echo "  docker: make dev-up"; exit 1; fi; \
	echo "dev-deps-check OK"

# ---------- Scaffolding ----------

.PHONY: new-endpoint
new-endpoint: ## 拷贝 5 文件骨架（handler/service/repo/model/task）；NAME=Order
	@if [ -z "$(NAME)" ]; then \
		echo "usage: make new-endpoint NAME=Order"; \
		exit 1; \
	fi
	@bash scripts/new-endpoint.sh "$(NAME)"

.PHONY: mod-upgrade
mod-upgrade: ## 干跑：列出可升的直接依赖（patch/minor）。APPLY=1 真升 + verify + 失败回滚
	@bash scripts/mod-upgrade.sh

# ---------- OpenAPI codegen ----------

OAPI_SPEC   := api/openapi.yaml
OAPI_CFG    := api/oapi-codegen.yaml
OAPI_OUTPUT := internal/oapi/oapi.gen.go

.PHONY: oapi-install
oapi-install: ## 仅校验/安装 oapi-codegen（pin 版本，不匹配会重装）
	@$(MAKE) --no-print-directory _ensure-oapi-codegen

.PHONY: oapi
oapi: oapi-install ## 从 api/openapi.yaml 生成 internal/oapi/oapi.gen.go
	@mkdir -p $(dir $(OAPI_OUTPUT))
	oapi-codegen -config $(OAPI_CFG) $(OAPI_SPEC)
	@echo "generated: $(OAPI_OUTPUT)"

.PHONY: architecture-verify
architecture-verify: ## 校验分层 import 边界（gin / gorm 包外溢、pkg→internal 反向依赖、service/handler 误用 context.Background）
	@bash scripts/architecture-verify.sh

.PHONY: docs-verify
docs-verify: ## 校验 AGENTS.md / CLAUDE.md 共享段保持同步
	@bash scripts/docs-verify.sh

.PHONY: docs-deploy-check
docs-deploy-check: ## 校验 docs/deploy.md 与 deploy/systemd/*.service 一致
	@bash scripts/deploy-doc-verify.sh

.PHONY: docs-errcodes
docs-errcodes: ## 重新生成 docs/errcodes.md（源：pkg/errcode + pkg/response.MessageFor）
	$(GO) run scripts/gen-errcodes.go

.PHONY: docs-errcodes-verify
docs-errcodes-verify: docs-errcodes ## 校验 docs/errcodes.md 与代码同步
	@if ! git diff --quiet -- docs/errcodes.md; then \
		echo ""; \
		echo "ERROR: docs/errcodes.md is out of sync."; \
		echo "       Run 'make docs-errcodes' and commit the result."; \
		echo ""; \
		git --no-pager diff -- docs/errcodes.md | head -40; \
		exit 1; \
	fi
	@echo "docs-errcodes-verify: docs/errcodes.md is in sync."

.PHONY: oapi-verify
oapi-verify: oapi ## 校验生成产物与 yaml 一致（CI / 提交前用）
	@if ! git diff --quiet -- $(OAPI_OUTPUT); then \
		echo ""; \
		echo "ERROR: $(OAPI_OUTPUT) is out of sync with $(OAPI_SPEC)."; \
		echo "       Run 'make oapi' and commit the result."; \
		echo ""; \
		git --no-pager diff -- $(OAPI_OUTPUT) | head -40; \
		exit 1; \
	fi
	@echo "oapi-verify: $(OAPI_OUTPUT) is in sync with $(OAPI_SPEC)."

.PHONY: tidy
tidy: ## go mod tidy + verify
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: tidy-verify
tidy-verify: ## 校验 go.mod / go.sum 与 go mod tidy 结果一致（防漂移）
	@tmpdir=$$(mktemp -d); \
	cp go.mod go.sum "$$tmpdir"/; \
	$(GO) mod tidy >/dev/null 2>&1 || { \
		echo "tidy-verify: 'go mod tidy' failed"; \
		cp "$$tmpdir/go.mod" "$$tmpdir/go.sum" .; \
		rm -rf "$$tmpdir"; \
		exit 1; \
	}; \
	if ! diff -q "$$tmpdir/go.mod" go.mod >/dev/null || \
	   ! diff -q "$$tmpdir/go.sum" go.sum >/dev/null; then \
		echo "tidy-verify: go.mod / go.sum out of sync with 'go mod tidy'"; \
		echo "--- go.mod diff ---"; diff "$$tmpdir/go.mod" go.mod || true; \
		echo "--- go.sum diff ---"; diff "$$tmpdir/go.sum" go.sum || true; \
		cp "$$tmpdir/go.mod" "$$tmpdir/go.sum" .; \
		rm -rf "$$tmpdir"; \
		exit 1; \
	fi; \
	rm -rf "$$tmpdir"; \
	echo "tidy-verify: go.mod / go.sum are tidy."

# ---------- 本地运行（三进程入口） ----------

.PHONY: run-api
run-api: ## 启动 API（占用 API_PORT，默认 3000）。端口占用会直接报错退出
	@if lsof -ti:$(API_PORT) >/dev/null 2>&1; then \
		echo "ERROR: port $(API_PORT) is busy. Run 'make stop-api' to free it, or set API_PORT=<other>."; \
		exit 1; \
	fi
	$(GO) run ./cmd/api

.PHONY: stop-api
stop-api: ## 显式终止占用 API_PORT 的进程（kill -9）
	@pids=$$(lsof -ti:$(API_PORT) 2>/dev/null); \
	if [ -z "$$pids" ]; then \
		echo "port $(API_PORT) is free, nothing to stop."; \
	else \
		echo "killing pids on port $(API_PORT): $$pids"; \
		kill -9 $$pids; \
	fi

.PHONY: run-worker
run-worker: ## 启动 Asynq 消费者
	$(GO) run ./cmd/worker

.PHONY: _ensure-air
_ensure-air:
	@want="$(AIR_VERSION)"; \
	if ! command -v air >/dev/null 2>&1; then \
		echo "Installing air $$want..."; \
		$(GO) install github.com/air-verse/air@$$want; \
	else \
		echo "air: ok"; \
	fi

.PHONY: watch
watch: ## 用 air 热重载跑 API（保存 .go 文件自动重启）
	@$(MAKE) --no-print-directory _ensure-air
	@if lsof -ti:$(API_PORT) >/dev/null 2>&1; then \
		echo "ERROR: port $(API_PORT) is busy. Run 'make stop-api' to free it, or set API_PORT=<other>."; \
		exit 1; \
	fi
	air -c .air.toml

.PHONY: run-migrate
run-migrate: ## 跑迁移 up：应用全部待执行的 migrations/*.sql（需要 POSTGRES 配置）
	$(GO) run ./cmd/migrate -cmd up

.PHONY: migrate-down
migrate-down: ## 回滚最近一版迁移（破坏性，默认需确认；CI/脚本传 confirm=1 绕过）
	@if [ "$(confirm)" != "1" ]; then \
		printf "migrate-down 会执行最近一版的 -- +goose Down 段（可能 DROP TABLE / 删列，数据不可逆）。\n输入 yes 继续: "; \
		read ans; \
		if [ "$$ans" != "yes" ]; then echo "已取消。"; exit 1; fi; \
	fi
	$(GO) run ./cmd/migrate -cmd down

.PHONY: migrate-status
migrate-status: ## 打印各迁移的应用状态（需要 POSTGRES 配置）
	$(GO) run ./cmd/migrate -cmd status

.PHONY: migrate-create
# name 经 target-specific export 进环境变量 migrate_name，recipe 全程只引用 shell
# 变量 "$$migrate_name"，不把 $(name) 裸拼进 shell——含引号 / 分号 / 反引号的输入
# 无法打断脚本，再由 case 白名单 [a-z0-9_]+ 拒掉。
# 仍无法防御开发者自己写 `name='$(shell ...)'`：命令行变量里的 $(shell) 由 Make 在
# 解析期执行、早于任何 recipe，这是 Make 语言层行为。本地开发者命令，不当安全边界。
migrate-create: export migrate_name = $(name)
migrate-create: ## 新建空迁移文件（UTC 时间戳前缀）：make migrate-create name=add_email_to_examples
	@if [ -z "$$migrate_name" ]; then echo "用法: make migrate-create name=<描述>"; exit 1; fi
	@case "$$migrate_name" in \
		*[!a-z0-9_]*) echo "ERROR: name 只能含小写字母/数字/下划线 [a-z0-9_]，收到: $$migrate_name"; exit 1;; \
	esac
	@ts=$$(date -u +%Y%m%d%H%M%S); \
	f="migrations/$${ts}_$${migrate_name}.sql"; \
	printf -- '-- +goose Up\n\n-- +goose Down\n' > "$$f"; \
	echo "created $$f"

# ---------- 构建 ----------

# bin/ 是 order-only 依赖：只有不存在时才创建，存在时不强制重做。
# 不放进 .PHONY，否则它会每次重建。
$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

.PHONY: version
version: ## 打印当前会注入到二进制里的版本元数据
	@echo "VERSION    = $(VERSION)"
	@echo "COMMIT     = $(COMMIT)"
	@echo "BUILD_TIME = $(BUILD_TIME)"

.PHONY: build
build: build-api build-worker build-migrate ## 构建三个进程的二进制到 bin/（本机平台，注入版本）

.PHONY: build-api
build-api: | $(BIN_DIR) ## 构建 API
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(API_BIN) ./cmd/api

.PHONY: build-worker
build-worker: | $(BIN_DIR) ## 构建 Worker
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(WORKER_BIN) ./cmd/worker

.PHONY: build-migrate
build-migrate: | $(BIN_DIR) ## 构建 Migrate
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(MIGRATE_BIN) ./cmd/migrate

# ---------- 交叉编译 + 发布 artifact ----------
#
# 静态链接：CGO_ENABLED=0 + -tags netgo 让产物可以扔到 distroless / scratch
# 或者裸 Linux 上跑，不依赖 glibc 版本。-trimpath 去掉构建机的绝对路径，
# 让相同源码 + 相同 ldflags 能产出 reproducible 二进制。

DIST_DIR    := dist
LINUX_AMD64 := linux/amd64
LINUX_ARM64 := linux/arm64

# 内部辅助目标，由 release 调用：$1=GOOS/GOARCH，例如 build-cross/linux/amd64。
.PHONY: build-cross
build-cross:
	@if [ -z "$(TARGET)" ]; then echo "build-cross requires TARGET=os/arch"; exit 1; fi
	@os=$$(echo $(TARGET) | cut -d/ -f1); arch=$$(echo $(TARGET) | cut -d/ -f2); \
	out=$(DIST_DIR)/$(VERSION)/$${os}-$${arch}; \
	mkdir -p $$out/bin $$out/deploy/systemd; \
	for cmd in api worker migrate; do \
		printf '  → %s/%s/bin/%s\n' "$$os" "$$arch" "$$cmd"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build -trimpath -tags netgo \
				-ldflags="$(LDFLAGS)" \
				-o $$out/bin/$$cmd ./cmd/$$cmd; \
	done; \
	cp .env.example $$out/.env.example; \
	cp deploy/systemd/*.service $$out/deploy/systemd/ 2>/dev/null || true; \
	cp docs/deploy.md $$out/DEPLOY.md 2>/dev/null || true; \
	cp LICENSE $$out/LICENSE; \
	echo "$(VERSION)" > $$out/VERSION

.PHONY: build-linux-amd64
build-linux-amd64: ## 交叉编译 linux/amd64 静态二进制到 dist/<version>/linux-amd64/
	@$(MAKE) --no-print-directory build-cross TARGET=$(LINUX_AMD64)

.PHONY: build-linux-arm64
build-linux-arm64: ## 交叉编译 linux/arm64 静态二进制到 dist/<version>/linux-arm64/
	@$(MAKE) --no-print-directory build-cross TARGET=$(LINUX_ARM64)

.PHONY: build-linux
build-linux: build-linux-amd64 build-linux-arm64 ## 构建所有 Linux 平台二进制

.PHONY: release
release: build-linux ## 把 Linux 二进制打成 tarball + SHA256SUMS 到 dist/<version>/
	@cd $(DIST_DIR)/$(VERSION) && \
	for d in linux-amd64 linux-arm64; do \
		tar -czf go-skeleton-$(VERSION)-$$d.tar.gz $$d/; \
		printf '  → %s\n' "dist/$(VERSION)/go-skeleton-$(VERSION)-$$d.tar.gz"; \
	done && \
	shasum -a 256 go-skeleton-$(VERSION)-*.tar.gz > SHA256SUMS && \
	printf '\n=== SHA256SUMS ===\n' && cat SHA256SUMS

# ---------- 容器镜像（默认构建 cmd/api） ----------

DOCKER_IMAGE ?= go-skeleton
DOCKER_TAG   ?= dev
CMD_TARGET   ?= api

.PHONY: docker-build
docker-build: ## 构建 multi-stage 镜像（CMD_TARGET=api|worker|migrate）
	docker build \
		--build-arg CMD_TARGET=$(CMD_TARGET) \
		--build-arg VERSION='$(VERSION)' \
		--build-arg COMMIT='$(COMMIT)' \
		--build-arg BUILD_TIME='$(BUILD_TIME)' \
		-t $(DOCKER_IMAGE)-$(CMD_TARGET):$(DOCKER_TAG) .

.PHONY: docker-run
docker-run: ## 本地跑 API 镜像（依赖 make dev-up；host.docker.internal 通到宿主）
	docker run --rm -p 3000:3000 \
		-e POSTGRES=postgres://user:password@host.docker.internal:5432/app?sslmode=disable \
		-e REDIS_ADDR=host.docker.internal:6379 \
		$(DOCKER_IMAGE)-api:$(DOCKER_TAG)

# ---------- 检查 ----------

.PHONY: fmt
fmt: ## 格式化代码（gofumpt + gci，统一走 golangci-lint fmt，配置见 .golangci.yml）
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found, run: make init"; exit 1; \
	}
	golangci-lint fmt

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## golangci-lint run（需要先 make init）
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found, run: make init"; exit 1; \
	}
	golangci-lint run

# ---------- 安全扫描 ----------
#
# 跟 verify 解耦：CVE 库更新 / gosec 升级会让原来通过的代码突然失败，
# 把它绑进 verify 会让本地提交体验抖动。本地按需 `make sec`，CI 单跑。

.PHONY: _ensure-govulncheck
_ensure-govulncheck:
	@want="$(GOVULNCHECK_VERSION)"; \
	if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "Installing govulncheck $$want..."; \
		$(GO) install golang.org/x/vuln/cmd/govulncheck@$$want; \
	else \
		echo "govulncheck: ok"; \
	fi

.PHONY: _ensure-gosec
_ensure-gosec:
	@want="$(GOSEC_VERSION)"; \
	if ! command -v gosec >/dev/null 2>&1; then \
		echo "Installing gosec $$want..."; \
		$(GO) install github.com/securego/gosec/v2/cmd/gosec@$$want; \
	else \
		echo "gosec: ok"; \
	fi

.PHONY: vuln
vuln: ## govulncheck 扫已知 CVE
	@$(MAKE) --no-print-directory _ensure-govulncheck
	govulncheck ./...

.PHONY: gosec
gosec: ## gosec 静态扫描安全反模式（排除 oapi.gen.go / _test.go）
	@$(MAKE) --no-print-directory _ensure-gosec
	gosec -quiet -exclude-generated -exclude-dir=dist -exclude-dir=bin ./...

.PHONY: sec
sec: vuln gosec ## 安全扫描一站式（govulncheck + gosec）

# ---------- 测试 ----------
#
# 测试分两档：
#   - 单元测试：默认跑，不依赖外部资源（DB/Redis 用 mock 或 DryRun）。
#   - 集成测试：文件首行 `//go:build integration`，需要真实 Postgres+Redis，
#     默认 go test 会跳过它们，避免 CI / 队友本地跑不通。
#
# 写集成测试的模板：
#   //go:build integration
#   package xxx_test
#   func TestXxxIntegration_...(t *testing.T) { ... }   // 函数名必须含 "Integration"
# 然后 `make test-integration` 触发；需要先起 `make dev-up`。
#
# 命名约定（重要）：集成测试函数名必须含 "Integration"。test-integration 用
# `-run Integration` 收口，只跑集成测试本身——否则 `-tags=integration ./...`
# 会把所有单元测试也一起重跑，让集成 job 变成"单测+集成"的超集，既慢又会把
# 单测的偶发波动算到集成 job 头上。

.PHONY: test
test: ## 跑单元测试（不含 integration tag）
	$(GO) test ./...

.PHONY: test-integration
test-integration: ## 跑集成测试（需要 make dev-up 起 Postgres + Redis）；-run Integration 只跑集成测试，不重跑单测
	$(GO) test -tags=integration -count=1 -run Integration ./...

.PHONY: test-race
test-race: ## 跑单元测试并开启 race detector
	$(GO) test -race ./...

.PHONY: cover
cover: ## 生成覆盖率报告（coverage.out + coverage.html）
	$(GO) test -coverpkg=./internal/...,./pkg/... -coverprofile=./coverage.out ./internal/... ./pkg/...
	$(GO) tool cover -html=./coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

# ---------- 入口：提交前必跑 ----------

.PHONY: verify
verify: ## 提交前一站式校验（fmt + vet + test + lint + architecture-verify + tidy-verify + oapi-verify + docs-verify + docs-deploy-check + docs-errcodes-verify）
	@$(MAKE) --no-print-directory _verify-step STEP=fmt
	@$(MAKE) --no-print-directory _verify-step STEP=vet
	@$(MAKE) --no-print-directory _verify-step STEP=test
	@$(MAKE) --no-print-directory _verify-step STEP=lint
	@$(MAKE) --no-print-directory _verify-step STEP=architecture-verify
	@$(MAKE) --no-print-directory _verify-step STEP=tidy-verify
	@$(MAKE) --no-print-directory _verify-step STEP=oapi-verify
	@$(MAKE) --no-print-directory _verify-step STEP=docs-verify
	@$(MAKE) --no-print-directory _verify-step STEP=docs-deploy-check
	@$(MAKE) --no-print-directory _verify-step STEP=docs-errcodes-verify
	@printf '\033[32m=== verify OK ===\033[0m\n'

# Prints a banner before each step so AI assistants and humans can spot
# the failing one instantly. Exits with the underlying step's status.
.PHONY: _verify-step
_verify-step:
	@printf '\n\033[36m=== STEP: %s ===\033[0m\n' "$(STEP)"
	@$(MAKE) --no-print-directory $(STEP) || { \
		printf '\n\033[31m=== STEP FAILED: %s ===\033[0m\n' "$(STEP)"; \
		exit 1; \
	}

# ---------- 清理 ----------

.PHONY: clean
clean: ## 清理构建产物与覆盖率报告
	rm -rf $(BIN_DIR) coverage.out coverage.html

# 注意：oapi 生成产物 internal/oapi/oapi.gen.go 入库，clean 不删除它。
# 想重新生成请用 make oapi。
