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

.PHONY: help
help: ## 列出所有可用 target
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
	     /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ---------- 开发依赖 ----------

.PHONY: init
init: ## 安装本项目用到的辅助工具（golangci-lint）
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "Installing golangci-lint..."; \
		$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	}
	@echo "init done."

.PHONY: tidy
tidy: ## go mod tidy + verify
	$(GO) mod tidy
	$(GO) mod verify

# ---------- 本地运行（三进程入口） ----------

.PHONY: run-api
run-api: ## 启动 API（占用 API_PORT，默认 3000）
	-@lsof -ti:$(API_PORT) | xargs kill -9 2>/dev/null || true
	$(GO) run ./cmd/api

.PHONY: run-worker
run-worker: ## 启动 Asynq 消费者
	$(GO) run ./cmd/worker

.PHONY: run-migrate
run-migrate: ## 跑 GORM AutoMigrate（需要 POSTGRES 配置）
	$(GO) run ./cmd/migrate

# ---------- 构建 ----------

.PHONY: build
build: build-api build-worker build-migrate ## 构建三个进程的二进制到 bin/

.PHONY: build-api
build-api: ## 构建 API
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(API_BIN) ./cmd/api

.PHONY: build-worker
build-worker: ## 构建 Worker
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(WORKER_BIN) ./cmd/worker

.PHONY: build-migrate
build-migrate: ## 构建 Migrate
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(MIGRATE_BIN) ./cmd/migrate

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
verify: fmt vet test lint ## 提交前一站式校验（fmt + vet + test + lint）

# ---------- 清理 ----------

.PHONY: clean
clean: ## 清理构建产物与覆盖率报告
	rm -rf $(BIN_DIR) coverage.out coverage.html
