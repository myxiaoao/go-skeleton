# Runbook

执行清单，给 AI 编码助手和人类用。只列**机器可执行的命令**，不重复规则解释（规则去看 `AGENTS.md` / `CLAUDE.md`）。

> 想看"从克隆到 PR"的叙事性工作流（按时间线串起来）见 [`docs/development.md`](./development.md)；
> 本文档按场景查命令。

## 0. 第一次复制 skeleton

```sh
# 一键改 module 名 + service shortname 后再做后续步骤
./scripts/rename.sh github.com/your-org/your-service your-service
# 跑完后 git rm scripts/rename.sh && git commit
```

## 一次性环境准备

```sh
make init          # 装 / 校验 golangci-lint、oapi-codegen（pin 版本）
cp .env.example .env
```

依赖（Postgres + Redis）二选一启动：

### A. 用 docker compose（推荐——零配置，端口 / 凭证已对齐 `.env.example`）

```sh
make dev-up        # 起 Postgres + Redis 容器（后台）
make run-migrate   # 跑迁移 up（详见下方"本地起完整三进程"）
```

### B. 用本机已装的 Postgres + Redis（不用 docker）

如果本机已经有 Postgres / Redis 服务，或者更习惯用宿主进程跑依赖，跳过 `make dev-up`，自己装好后跑 `make dev-deps-check` 验证可连。

**macOS（Homebrew）**：

```sh
brew install postgresql@17 redis
brew services start postgresql@17
brew services start redis

# 建与 .env.example 对齐的 user / db：
createuser -s user 2>/dev/null || true
psql -d postgres -c "ALTER USER \"user\" WITH PASSWORD 'password';"
createdb -O user app
```

**Linux（apt / dnf）**：

```sh
# Debian / Ubuntu
sudo apt install -y postgresql-17 redis-server
sudo systemctl enable --now postgresql redis-server

# RHEL / CentOS / Fedora 类
sudo dnf install -y postgresql-server postgresql redis
sudo postgresql-setup --initdb
sudo systemctl enable --now postgresql redis

# 建与 .env.example 对齐的 user / db：
sudo -u postgres psql <<'SQL'
CREATE USER "user" WITH PASSWORD 'password';
CREATE DATABASE app OWNER "user";
SQL
```

验证可连 + 初始化表：

```sh
make dev-deps-check    # 探活 Postgres :5432 + Redis :6379
go run ./cmd/migrate
```

> 生产凭证**不要**用 `user/password`；这套默认值只是开发期为了零配置。改 `.env` 里的 `POSTGRES` DSN + `REDIS_PASSWORD` 后再 `make dev-deps-check`。

## 提交前必跑

```sh
make verify        # fmt + vet + test + lint + architecture-verify + env-verify + tidy-verify + oapi-verify + docs-verify + docs-deploy-check + docs-errcodes-verify（每步有横幅）
```

任意一步挂了，看最后一个红色 `=== STEP FAILED: xxx ===` 横幅就知道挂在哪。**不要用 `--no-verify` 跳过 hook**。

## 移除骨架自带的 Example 示例模块

复制骨架开始真业务前，把 Example 拔干净：

```sh
make drop-example   # 一次性脚本，需 clean checkout
```

脚本（`scripts/drop-example.go`）会：

- 删 `internal/{handler,service,repository,model,task}/example*.go` + 集成测试 + worker handler 测试
- 删 `migrations/2026...examples_table.sql`，留一个 `00010101000001_placeholder.sql` 让 `//go:embed *.sql` 不空（接入第一条真迁移后删它）
- 清掉 `internal/server.go` / `router.go` / `worker.go` / `handler/openapi.go` 里的 Example 装配 + 转发方法，**保留** `// NEH ...` 锚点供 `new-endpoint.sh` 后续注入
- 清掉 `api/openapi.yaml` 里 `/api/v1/examples`、`/examples/tasks` 路径 + 8 个 `Example*` schemas + `tags.example`
- 跑 `make oapi` 重新生成 `internal/oapi/oapi.gen.go`、`go mod tidy` 收敛依赖
- 跑构建 + 测试 + 静态校验子集（fmt/vet/test/lint/architecture-verify/env-verify/tidy-verify/docs-verify）确认不破坏

跑完手动 commit 后再跑一次 `make verify`，让 oapi-verify / docs-deploy-check / docs-errcodes-verify（这些比对工作树 vs HEAD）也变绿。然后 `./scripts/new-endpoint.sh <Name>` 接真业务。

> 注意：`new-endpoint.sh` 依赖 `internal/<layer>/example.go` 当模板。drop 跑过后模板就没了——再起新模块需从 git 历史 `git checkout <pre-drop-sha> -- internal/<layer>/example.go` 恢复模板，或干脆先 `git revert` drop-example 那条 commit。

## 新增一个 HTTP API endpoint

```sh
# 1. 一条命令生成 5 个分层文件 + 测试 stub + server.go / router.go 装配。
#    脚本按 internal/server.go 和 internal/router/router.go 里的 // NEH
#    锚点注释自动注入，不要手动改这些锚点行。
./scripts/new-endpoint.sh <Name>          # 如 ./scripts/new-endpoint.sh Order

# 2. 按脚本最后打印的提示补两件人工活：
#    a. api/openapi.yaml：加 paths + components.schemas（脚本给了 stub）
#    b. internal/handler/openapi.go::APIServer：加字段 + ServerInterface
#       方法骨架（脚本给了 stub）
#    然后跑：
make oapi

# 3. 业务字段调整：脚本生成的 *.go 是 example 模板的复刻，要按真实业
#    务改 model 字段、service 方法、repository SQL。
#    测试 stub 是 placeholder（Skip），按 internal/<layer>/example_test.go
#    的"标准库 testing + 手写 mock"风格补真实用例。

# 4. 验证
make verify
```

**编译期保险**：`internal/handler/openapi.go` 里有
`var _ oapi.ServerInterface = (*APIServer)(nil)`，OpenAPI yaml 与 APIServer
方法集漂移时 `go build` 直接失败，不依赖人 review。

## 新增一个 Asynq 异步任务

```sh
# 1. internal/task/<name>.go：定义任务类型常量 + payload struct + 工厂
#    a. payload 头部匿名嵌入 task.Header（提供 Version + TraceID），通过
#       task.NewHeader(traceID) 构造；新增字段加 omitempty 向后兼容，改
#       字段语义 / 删字段必须升 task.PayloadSchemaVersion + 同步
#       CurrentSupported.Max。
#    b. NewXxxTask 工厂用 task.DefaultOptions() 带默认 MaxRetry/Timeout，
#       业务有特殊需求时 append asynq.MaxRetry(N) / asynq.Timeout(d) 覆盖；
#       序列化走 task.MarshalPayload(TypeXxx, p) 让 error 自动带 task type。
# 2. internal/worker/handler.go：
#    a. 定义 typed processor 接口（如 XxxProcessor.ProcessXxx(ctx, payload) error），
#       不要回退到 interface{} —— 让 worker handler 调"有名有姓"的方法，
#       接错 service 时编译期就能发现。
#    b. 在 Deps 上加一个 Xxx 字段，类型用上一步的接口。
#    c. RegisterHandlers 里 mux.HandleFunc(task.TypeXxx, deps.HandleXxxTask)。
#    d. 写一个 HandleXxxTask：解 payload → task.CheckHeader 校验 schema
#       version（超界返 error 让 asynq 重试，不要静默吞）→ 调
#       deps.Xxx.ProcessXxx(ctx, p)。
# 3. service：给对应 Service 加一个方法实现该接口（落库 / 调外部系统）。
# 4. internal/worker.go::buildWorkerDeps：把 service 实例注入 Deps.Xxx。
# 5. service 一侧通过 ExampleQueue 接口发布任务（不要直接拿 *asynq.Client）。
#    业务键稳定的任务用 asynq.TaskID(task.BuildTaskID("ns", keys...)) 永
#    久去重（订单状态推进等）；短窗口防抖用 asynq.Unique(ttl)（用户点击
#    防抖、定时拉取去重）。两种语义不同，不要混用。
#    BuildTaskID 超长时会用"可读前缀 + SHA-256 后缀"控制在 1KB 内，不会
#    简单截断；如果经常命中上限，说明业务 key 设计太大，需要收敛。
make verify
```

注意：Deps.Example 未注入时 RegisterHandlers 会回填 noopExampleProcessor 兜底
（避免模板态 nil deref），但它只打 warn 不真处理；真实业务必须显式注入，
否则任务会被"消费"但没有副作用。

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

## OpenAPI 破坏性变更检查（PR 前 / 发版前）

`make oapi-verify` 只检"yaml 和生成产物对齐"，不判"yaml 改动是不是破坏兼容"。后者走 `oapi-breaking`：

```sh
make oapi-breaking                              # 默认对比 origin/master
OAPI_BREAKING_BASE_REF=v0.1.0 make oapi-breaking # 对比某个 tag
OAPI_ALLOW_BREAKING=1 make oapi-breaking         # 故意 expand-contract 时跳过
```

底层用 [oasdiff](https://github.com/oasdiff/oasdiff)，`--fail-on ERR` 只有确凿 breaking（删 endpoint、改返回 schema 字段类型等）才退出非零。CI 在 PR 上跑同样命令；故意做 breaking 时在 PR 描述里写明缘由并在该 step env 设 `OAPI_ALLOW_BREAKING=1`，或本地 commit 时同时回写约定。

> `make verify` **不**包含 `oapi-breaking`：本地不一定有 fresh `origin/master`，且 expand-contract 阶段会故意 breaking。它定位是"PR / 发版前"门禁。

## 本地起完整三进程

**推荐**（一条命令搞定，前台跟随两路日志，Ctrl-C 优雅停）：

```sh
make dev-up        # 起依赖（已起则跳过）
make dev-all       # 探活 → 迁移 → 并发起 API + Worker（前缀 [api] / [worker]）
```

`make dev-all` 是 `scripts/dev-all.sh` 的薄封装，做 4 件事：
- `dev-deps-check` 探活 Postgres + Redis（不可达直接 fail，不自动起 docker）
- `go run ./cmd/migrate -cmd up`（迁移完才放行后续）
- 并发起 API + Worker，stdout 前缀化方便区分
- Ctrl-C 触发优雅停：先 SIGTERM，等 `GRACEFUL_SHUTDOWN_TIMEOUT`（默认 15s）再 SIGKILL 兜底

任一子进程退出会带走另一个（先挂的 exit code 透传给 make）。

**手动分步**（需要单独 attach / 调试某个进程时用）：

```sh
make dev-up
go run ./cmd/migrate           # goose up，应用 migrations/ 待执行迁移（建表）
make run-api &                 # 端口 3000
make run-worker &              # 消费 Asynq
```

> 迁移命令：`make run-migrate`（up）/ `make migrate-down`（回滚一版）/
> `make migrate-status`（看状态）/ `make migrate-create name=add_xxx`（新建空迁移）。
> 改表结构走"写 `migrations/<序号>_<描述>.sql` + 跑 `make run-migrate`"，不要改 model
> 等 AutoMigrate（已移除）。

### 迁移文件 lint（commit 前会自动跑）

`make verify` 通过 `migrations/migrations_test.go` 强制三道门：

1. **文件名**：`<14 位 UTC 时间戳>_<snake_case>.sql`（`make migrate-create` 默认输出格式）。少时分秒会同日撞版本号；带大写 / 连字符跨工具脚本兼容性差。
2. **goose 注解**：必须同时含 `-- +goose Up` 和 `-- +goose Down`。
3. **破坏性 DDL 必须标注**：检测 Up 段里的 `DROP TABLE` / `DROP COLUMN` / `DROP CONSTRAINT` / `ALTER COLUMN TYPE` / `SET NOT NULL` / `RENAME COLUMN|TO` / `TRUNCATE`，命中后必须配 `-- breaking: <reason>`（或 `-- +breaking <reason>`）显式标注：

   ```sql
   -- +goose Up
   -- breaking: 配合 v2.4 服务下线，old_email 列已无引用方
   ALTER TABLE users DROP COLUMN old_email;
   ```

   挡住"无声 DROP COLUMN"这类回滚困难的提交；真要 expand-contract 时 marker 是"自证已做风险评估"的痕迹。配合 `docs/deploy.md` §5 升级 / 回滚段使用。

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
| `c.ClientIP()` 取错 | 没配 `TRUSTED_PROXIES`；见 README "上线前检查清单" |
| `/api/v1/auth/token` 返 `SERVICE_DISABLED` | 设计如此（`AUTH_DEV_TOKEN_ENABLED=false` 或 JWT 没配）|

## 验收新模块的最小自检

```sh
make verify                                        # 项目级一站式
go test -run TestNewModule ./internal/... -v       # 新增模块的测试单独跑
grep -rn "TODO\|FIXME" --include="*.go" internal/  # 没留 TODO
```

## 线上排障：开 pprof

pprof debug 端点默认关闭。需要现场抓 CPU/heap profile 时临时打开，**不要**长开。

1. SSH 到目标机器，设环境变量 + 重启 API：
   ```sh
   PPROF_ENABLED=true PPROF_ADDR=127.0.0.1:6060 systemctl restart go-skeleton-api
   ```
2. 本地通过 SSH 隧道访问（pprof 只绑回环，禁止公网暴露）：
   ```sh
   ssh -L 6060:127.0.0.1:6060 prod-host
   # 另一个终端
   go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30   # CPU
   go tool pprof http://127.0.0.1:6060/debug/pprof/heap                 # heap
   curl http://127.0.0.1:6060/debug/pprof/goroutine?debug=2             # goroutine dump
   ```
3. 排障完关掉 PPROF_ENABLED，重启 API。

## 线上 P0 排错速查

| 现象 | 第一个该跑的命令 |
| --- | --- |
| API 起不来 | `journalctl -u go-skeleton-api -n 200 --no-pager` |
| 端口占用 | `ss -ltnp \| grep :3000` |
| OOM 重启 | `journalctl -u go-skeleton-api \| grep -i oom` |
| DB 慢查询 | `psql -c "SELECT pid, now()-query_start AS dur, query FROM pg_stat_activity WHERE state='active' ORDER BY dur DESC LIMIT 10"` |
| Worker 不消费 | `curl http://127.0.0.1:8980` (Asynqmon)；或 `redis-cli -n 6 LLEN asynq:queues:default` |
| trace_id 找日志 | `journalctl -u go-skeleton-api -o cat \| jq 'select(.trace_id=="XXX")'` |
| 想知道版本 | `/opt/go-skeleton/bin/api -version` 或 `curl /health \| jq .build` |
| /health degraded | Redis 抖动；看 `journalctl -u go-skeleton-api \| grep -i redis` |
| /health 503 | DB 不可达；看 Postgres 进程 + 连接数（`pg_stat_activity`） |
| watchdog 重启 | `journalctl -u go-skeleton-api \| grep -i watchdog`；进程挂死或 GC pause 过长 |
| FD 耗尽 (EMFILE) | `cat /proc/$(pgrep -f bin/api)/limits`；确认 LimitNOFILE=65535 生效 |

## 日志字段速查

zap 输出 JSON 走 stdout，systemd 收到 journald。`journalctl -u go-skeleton-api -o cat | jq` 之后能按下面这些字段过滤。

**通用字段（每条日志都带）**：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `service` | string | 进程名（`api` / `worker` / `migrate`），多服务采集到同一索引时用来区分 |
| `level` | string | `info` / `warn` / `error`；级别由 `LOG_LEVEL` env 控制 |
| `ts` | float | 秒级 UNIX 时间戳（ISO8601 由 encoder 决定） |
| `caller` | string | `file.go:line`，定位日志来源 |
| `msg` | string | 日志正文 |
| `trace_id` | string | 请求链路 ID；HTTP 来自 `X-Request-ID` 或自动生成，Asynq 来自任务 payload |

**HTTP 审计字段（middleware.TraceLogger，每个请求一条 `http request completed`）**：

| 字段 | 来源 |
| --- | --- |
| `method` | HTTP 方法（GET / POST / ...） |
| `path` | 请求路径 |
| `status` | int 响应状态码 |
| `latency` | Duration，请求总耗时 |
| `client_ip` | gin 解析后的客户端 IP（受 `TRUSTED_PROXIES` 影响） |

**Asynq 任务字段（worker 进程，`asynq task started/finished/failed`）**：

| 字段 | 含义 |
| --- | --- |
| `task` | 任务类型常量（如 `example:run`） |
| `task_id` | Asynq 分配的 task ID |
| `queue` | 任务所属队列名（critical / default / low） |
| `retry_count` | 当前是第几次重试 |
| `attempt` | 同上但 `RetryDelayFunc` 写入（n+1） |
| `retry_after` | Duration，下次重试延迟 |
| `trace_source` | `request`（来自 HTTP）/ `asynq_task`（合成 trace） |
| `error` | 失败原因 |

**Panic 兜底字段（middleware.Recovery）**：

| 字段 | 含义 |
| --- | --- |
| `error` | panic 抛出的对象（zap.Any，可能是 string / error / 任意类型） |
| `stacktrace` | ByteString，`runtime/debug.Stack()` 完整调用栈 |

**常用 jq 查询**：

```sh
# 按 trace_id 找一条请求的全链路
journalctl -u go-skeleton-api -u go-skeleton-worker -o cat \
  | jq 'select(.trace_id=="<TRACE_ID>")'

# 看所有 5xx
journalctl -u go-skeleton-api -o cat | jq 'select(.status >= 500)'

# 看慢请求（> 1s）
journalctl -u go-skeleton-api -o cat | jq 'select(.latency > 1000000000)'

# 看死信前的 retry 历史
journalctl -u go-skeleton-worker -o cat | jq 'select(.task=="example:run" and .retry_count > 0)'

# 抓 panic
journalctl -u go-skeleton-api -o cat | jq 'select(.msg=="panic recovered")'
```

## Prometheus metrics

API 进程默认在 `/metrics` 暴露 Prometheus 抓取端点（关闭走 `METRICS_ENABLED=false`）。**不**走 BearerAuth；生产靠网络层 + LB allowlist 保护，不要暴露公网。

**推荐生产配 `METRICS_ADDR`**（如 `127.0.0.1:9090` 或内网 IP）：非空时业务 engine 不再挂 `/metrics`，由独立 `http.Server` 监听独立端口——业务端口公网暴露不会顺带泄露指标，与业务在 L4 层就隔离。空值（默认）维持把 `/metrics` 挂在业务 engine 同端口，`APP_ENV=production` 下会触发启动期 warn。Prometheus scrape config 抓 `METRICS_ADDR` 的端口即可。

主要指标（namespace `go_skeleton_api_*`）：

| 指标 | 类型 | 用途 |
| --- | --- | --- |
| `http_requests_total{method,route,status}` | counter | QPS / 错误率（`status=~"5.."` 算错误） |
| `http_request_duration_seconds_bucket` | histogram | p50 / p95 / p99 延迟（`histogram_quantile(0.95, ...)`）|
| `http_requests_in_flight` | gauge | 当前并发请求数 |
| `asynq_queue_size{queue}` | gauge | 队列总任务数 |
| `asynq_queue_pending{queue}` | gauge | 待处理任务数 |
| `asynq_queue_retry{queue}` | gauge | 等待重试的任务数 |
| `asynq_queue_archived{queue}` | gauge | **死信任务数**（耗尽重试） |
| `asynq_queue_latency_seconds{queue}` | gauge | 最老 pending 任务的年龄 |
| `go_*` / `process_*` | mixed | Go runtime + 进程资源（goroutines / GC / FD / RSS） |

本地起 API 后验证：

```sh
curl -s http://127.0.0.1:3000/metrics | grep go_skeleton_api_
```

Prometheus 抓取最小配置：

```yaml
scrape_configs:
  - job_name: go-skeleton-api
    static_configs:
      - targets: ['go-skeleton-api:3000']
```

## Asynq 死信 / 投毒任务处理

任务在重试到 `asynq.MaxRetry` 上限仍失败后会被 Asynq 挪到 **archived** 队列（死信队列），不再自动重试。`asynq_queue_archived` gauge 持续 >0 就该有人处理；可以配 Prometheus alert：

```yaml
- alert: AsynqArchivedTasks
  expr: go_skeleton_api_asynq_queue_archived > 0
  for: 10m
  annotations:
    summary: "Asynq queue {{ $labels.queue }} has dead-letter tasks"
```

### 列出 + 重投 + 删除

最简单的工具是 **Asynqmon** web UI（`make dev-asynqmon` 起本地 UI，监听 `:8980`），生产可以本机起后通过 SSH 隧道访问。

命令行（生产机上）：

```sh
# 装 asynq CLI 一次（不入项目仓库）
go install github.com/hibiken/asynq/tools/asynq@latest

# 列出某队列的死信任务
asynq task ls --queue=default --state=archived

# 重投某条任务
asynq task run --queue=default <task_id>

# 删除某条任务（确认是脏数据再删，**删了就找不回**）
asynq task delete --queue=default <task_id>

# 一次性重投全部
asynq task run --queue=default --state=archived --all
```

### 业务侧：写新任务时的幂等约定

死信常常源于**重投同一任务产生副作用**（重复扣款、重复发邮件）。新增任务类型时强烈建议：

1. **选对去重机制**（两种语义不同，不要混用）：
   - 业务键稳定 + 永久全局唯一 → `asynq.TaskID(task.BuildTaskID("order", "shipped", orderID))`，
     同 ID 已入队会返 `asynq.ErrTaskIDConflict`。`BuildTaskID` 超长时保留可读前缀并追加 SHA-256 后缀，避免简单截断误碰撞；订单状态推进、用户操作日志走这条。
   - 短窗口防抖 → `asynq.Unique(5 * time.Minute)`，asynq 自动按 payload 哈希
     在 TTL 内去重，超过 TTL 同 payload 可再次入队。用户点按钮、定时拉取走这条。
2. **消费端再做一次幂等检查**（双保险）：handler 第一步先用业务键查
   "是否已处理"，已处理直接 return nil（不算失败，避免 archived 队列堆数据）。
   Asynq 的去重只能挡住"重复入队"，挡不住"任务跑到一半 crash 后被重试"。
3. **payload 嵌入 `task.Header`**（包含 Version + TraceID），版本不兼容
   直接拒绝任务而不是凭空猜字段，避免新老 worker 同时跑期间的 schema 漂移。
4. 业务自己的幂等 key 放 payload 字段，不要依赖 Asynq 分配的内部 task ID
   （那个跟业务无关、不便日志追踪）。

参考骨架 `internal/task/example.go` 的 `MaxRetry(5)` 约定；超过 5 次仍失败的会进 archived，记得跟踪 alert。
