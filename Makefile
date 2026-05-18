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
LDFLAGS  ?= -s -w

# 工具链版本固定。升级时改这里 + 跑 make init 重新装，让 CI / 队友复现一致。
GOLANGCI_LINT_VERSION ?= v2.12.2
OAPI_CODEGEN_VERSION  ?= v2.7.0

.PHONY: help
help: ## 列出所有可用 target
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
	     /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ---------- 开发依赖 ----------

.PHONY: init
init: ## 安装/对齐辅助工具到 pin 版本（已是 pin 版本则跳过）
	@$(MAKE) --no-print-directory _ensure-golangci-lint
	@$(MAKE) --no-print-directory _ensure-oapi-codegen
	@echo "init done."

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

.PHONY: run-migrate
run-migrate: ## 跑 GORM AutoMigrate（需要 POSTGRES 配置）
	$(GO) run ./cmd/migrate

# ---------- 构建 ----------

# bin/ 是 order-only 依赖：只有不存在时才创建，存在时不强制重做。
# 不放进 .PHONY，否则它会每次重建。
$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

.PHONY: build
build: build-api build-worker build-migrate ## 构建三个进程的二进制到 bin/

.PHONY: build-api
build-api: | $(BIN_DIR) ## 构建 API
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(API_BIN) ./cmd/api

.PHONY: build-worker
build-worker: | $(BIN_DIR) ## 构建 Worker
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(WORKER_BIN) ./cmd/worker

.PHONY: build-migrate
build-migrate: | $(BIN_DIR) ## 构建 Migrate
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(MIGRATE_BIN) ./cmd/migrate

# ---------- 容器镜像（默认构建 cmd/api） ----------

DOCKER_IMAGE ?= go-skeleton
DOCKER_TAG   ?= dev
CMD_TARGET   ?= api

.PHONY: docker-build
docker-build: ## 构建 multi-stage 镜像（CMD_TARGET=api|worker|migrate）
	docker build \
		--build-arg CMD_TARGET=$(CMD_TARGET) \
		-t $(DOCKER_IMAGE)-$(CMD_TARGET):$(DOCKER_TAG) .

.PHONY: docker-run
docker-run: ## 本地跑 API 镜像（依赖 make dev-up；host.docker.internal 通到宿主）
	docker run --rm -p 3000:3000 \
		-e POSTGRES=postgres://user:password@host.docker.internal:5432/app?sslmode=disable \
		-e REDIS_ADDR=host.docker.internal:6379 \
		$(DOCKER_IMAGE)-api:$(DOCKER_TAG)

# ---------- 检查 ----------

.PHONY: fmt
fmt: ## gofmt -s -w 全部代码
	$(GO) fmt ./...

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## golangci-lint run（需要先 make init）
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found, run: make init"; exit 1; \
	}
	golangci-lint run

# ---------- 测试 ----------

.PHONY: test
test: ## 跑全部测试
	$(GO) test ./...

.PHONY: test-race
test-race: ## 跑测试并开启 race detector
	$(GO) test -race ./...

.PHONY: cover
cover: ## 生成覆盖率报告（coverage.out + coverage.html）
	$(GO) test -coverpkg=./internal/...,./pkg/... -coverprofile=./coverage.out ./internal/... ./pkg/...
	$(GO) tool cover -html=./coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

# ---------- 入口：提交前必跑 ----------

.PHONY: verify
verify: fmt vet test lint oapi-verify ## 提交前一站式校验（fmt + vet + test + lint + oapi-verify）

# ---------- 清理 ----------

.PHONY: clean
clean: ## 清理构建产物与覆盖率报告
	rm -rf $(BIN_DIR) coverage.out coverage.html

# 注意：oapi 生成产物 internal/oapi/oapi.gen.go 入库，clean 不删除它。
# 想重新生成请用 make oapi。
