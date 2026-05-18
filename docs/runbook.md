# Runbook

执行清单，给 AI 编码助手和人类用。只列**机器可执行的命令**，不重复规则解释（规则去看 `AGENTS.md` / `CLAUDE.md`）。

## 一次性环境准备

```sh
make init          # 装 / 校验 golangci-lint、oapi-codegen（pin 版本）
cp .env.example .env
make dev-up        # docker compose 起 Postgres + Redis
go run ./cmd/migrate
```

## 提交前必跑

```sh
make verify        # fmt + vet + test + lint + oapi-verify（每步有横幅）
```

任意一步挂了，看最后一个红色 `=== STEP FAILED: xxx ===` 横幅就知道挂在哪。**不要用 `--no-verify` 跳过 hook**。

## 新增一个 HTTP API endpoint

```sh
# 1. 改契约
$EDITOR api/openapi.yaml

# 2. 生成代码（产物入库）
make oapi

# 3. 加分层文件（按 example 模板）
# - internal/handler/<name>.go
# - internal/service/<name>.go
# - internal/repository/<name>.go
# - internal/model/<name>.go

# 4. 在 internal/server.go::newHTTPHandlers 装配
# 5. 在 internal/router/router.go::Dependencies + registerXxxRoutes 注册
# 6. 编译期合约保险线会自动检查（var _ oapi.ServerInterface = (*APIServer)(nil)）

# 7. 加测试，然后
make verify
```

## 新增一个 Asynq 异步任务

```sh
# 1. internal/task/<name>.go：定义任务类型常量 + payload struct
# 2. internal/worker/handler.go::RegisterHandlers 注册消费 handler
# 3. service 通过 ExampleQueue 接口发布（不要直接拿 *asynq.Client）
make verify
```

## 跑测试

```sh
go test ./...                                       # 全跑
go test ./internal/service/... -run TestCreate -v   # 单包 + 单测
make test-race                                      # 带 race detector
make cover && open coverage.html                    # 覆盖率
```

## 改 OpenAPI 后忘了重新生成？

`make verify` 的 `oapi-verify` 步骤会用 `git diff --quiet` 检查 `internal/oapi/oapi.gen.go`。提示 out of sync 时：

```sh
make oapi
git add internal/oapi/oapi.gen.go
```

## 本地起完整三进程

```sh
make dev-up
go run ./cmd/migrate           # 一次性，建表
make run-api &                 # 端口 3000
make run-worker &              # 消费 Asynq
```

停服：

```sh
make stop-api
# worker 自己 Ctrl-C；它会两阶段优雅停（Stop + Shutdown）
make dev-down                  # 数据卷保留
make dev-reset                 # 数据卷销毁（破坏性）
```

## 出二进制 release artifact

详细部署步骤见 [`docs/deploy.md`](./deploy.md)。

```sh
make build-linux                                # 交叉编译 amd64 + arm64 静态二进制
                                                # → dist/<version>/linux-{amd64,arm64}/
make release                                    # 顺便打 tarball + SHA256SUMS
                                                # → dist/<version>/go-skeleton-<version>-linux-*.tar.gz
make version                                    # 看当前会注入哪个版本号

# 单独跑某个架构：
make build-linux-amd64
make build-linux-arm64

# 看二进制版本：
./bin/api -version
./bin/worker -version
./bin/migrate -version

# CI 自动发版：推一个 v* tag 会触发 .github/workflows/release.yml
git tag v0.2.0 && git push origin v0.2.0
```

## 打容器镜像

```sh
make docker-build              # 默认 cmd/api，产物 go-skeleton-api:dev
make docker-run                # 本地跑，连 host.docker.internal 上的 dev 依赖

CMD_TARGET=worker make docker-build   # 同一份 Dockerfile 打 worker
CMD_TARGET=migrate make docker-build  # 同一份 Dockerfile 打 migrate
```

## 排错 cheat sheet

| 现象 | 先看 |
| --- | --- |
| `make verify` 红 | 最后一个 `=== STEP FAILED: xxx ===` 横幅指向的步骤 |
| `oapi-verify` 报 out of sync | `make oapi` 然后 commit 生成产物 |
| `make run-api` 提示端口占用 | `make stop-api` 释放，或 `API_PORT=3001 make run-api` |
| 测试里日志刷屏 | 测试 `init()` 漏了 `applog.SetLogger(zap.NewNop())` |
| handler 测试 binding 报错文案为空 | 测试 `init()` 漏了 `validator.InitValidator()` |
| `c.ClientIP()` 取错 | 没配 `TRUSTED_PROXIES`；上线检查清单第 6 项 |
| `/api/v1/auth/token` 返 `SERVICE_DISABLED` | 设计如此（`AUTH_DEV_TOKEN_ENABLED=false` 或 JWT 没配）|

## 验收新模块的最小自检

```sh
make verify                                        # 项目级一站式
go test -run TestNewModule ./internal/... -v       # 新增模块的测试单独跑
grep -rn "TODO\|FIXME" --include="*.go" internal/  # 没留 TODO
```
