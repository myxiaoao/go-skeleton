# 二进制部署指南

适用场景：没有 Docker / K8s 平台，需要把 `go-skeleton` 直接装到 Linux 主机上（裸机或 VM）。

镜像部署见 [`Dockerfile`](../Dockerfile) 与 README 的 Docker 章节；这里只讲二进制路径。

## 0. 准备工作

目标主机要求：

- Linux x86_64 或 ARM64（amd64 / arm64）
- systemd（CentOS 7+, Debian 10+, Ubuntu 18.04+, RHEL 8+ 都满足）
- Postgres 与 Redis 可达（同机或网络可通即可）

二进制是**静态链接**的（`CGO_ENABLED=0 + netgo`），不依赖 glibc 版本，可以扔到 distroless / Alpine / 任意发行版上跑。

## 1. 获取二进制

### 1a. 从 GitHub Releases 下载（推荐）

```sh
# 替换 VERSION 为目标版本（例如 v0.2.0），ARCH 为 amd64 或 arm64
VERSION=v0.2.0
ARCH=amd64
curl -L -o go-skeleton.tar.gz \
  https://github.com/myxiaoao/go-skeleton/releases/download/${VERSION}/go-skeleton-${VERSION}-linux-${ARCH}.tar.gz
curl -L -o SHA256SUMS \
  https://github.com/myxiaoao/go-skeleton/releases/download/${VERSION}/SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing
```

### 1b. 自己构建

```sh
# 在开发机或 CI 上：
git checkout v0.2.0
make build-linux         # 产物在 dist/<version>/{linux-amd64,linux-arm64}/
# 或者 make release 顺便打 tarball + SHA256SUMS
make release
```

`make build-linux` 输出目录结构：

```
dist/<version>/linux-amd64/
├── bin/
│   ├── api
│   ├── worker
│   └── migrate
├── deploy/systemd/
│   ├── go-skeleton-api.service
│   ├── go-skeleton-worker.service
│   └── go-skeleton-migrate.service
├── .env.example
├── DEPLOY.md
├── LICENSE
└── VERSION
```

## 2. 系统初始化（首次部署）

```sh
# 1) 创建运行账户与目录
sudo useradd --system --no-create-home --shell /usr/sbin/nologin go-skeleton
sudo mkdir -p /opt/go-skeleton/bin /etc/go-skeleton
sudo chown -R go-skeleton:go-skeleton /opt/go-skeleton

# 2) 解压二进制（在目标机上）
tar -xzf go-skeleton-${VERSION}-linux-${ARCH}.tar.gz
cd linux-${ARCH}

# 3) 安装二进制
sudo install -m 0755 -o go-skeleton -g go-skeleton bin/api     /opt/go-skeleton/bin/api
sudo install -m 0755 -o go-skeleton -g go-skeleton bin/worker  /opt/go-skeleton/bin/worker
sudo install -m 0755 -o go-skeleton -g go-skeleton bin/migrate /opt/go-skeleton/bin/migrate

# 4) 安装 systemd unit
sudo install -m 0644 deploy/systemd/go-skeleton-api.service     /etc/systemd/system/
sudo install -m 0644 deploy/systemd/go-skeleton-worker.service  /etc/systemd/system/
sudo install -m 0644 deploy/systemd/go-skeleton-migrate.service /etc/systemd/system/
sudo systemctl daemon-reload

# 5) 配置文件（含敏感信息，限权）
sudo install -m 0640 -o root -g go-skeleton .env.example /etc/go-skeleton/.env
sudo $EDITOR /etc/go-skeleton/.env
```

`/etc/go-skeleton/.env` **必须**在生产前替换：

- `JWT_SECRET`（至少 32 字节随机值：`openssl rand -base64 48`）
- `AUTH_DEV_TOKEN_ENABLED=false`
- `GIN_MODE=release`
- `LOG_FORMAT=json`
- `CORS_ALLOW_ORIGINS` 显式枚举
- `TRUSTED_PROXIES` 配置成实际 LB 网段
- `POSTGRES` / `REDIS_ADDR` 指向真实实例

完整清单见 [README "Production Checklist"](../README.md#production-checklist)。

## 3. 启动顺序

```sh
# 1) 一次性跑 migration
sudo systemctl start go-skeleton-migrate.service
sudo journalctl -u go-skeleton-migrate.service -n 30 --no-pager
# 看到 "migrations completed" 才继续

# 2) 启动 API + Worker
sudo systemctl enable --now go-skeleton-api.service
sudo systemctl enable --now go-skeleton-worker.service

# 3) 确认状态
sudo systemctl status go-skeleton-api.service
sudo systemctl status go-skeleton-worker.service

# 4) 健康检查
curl -fsS http://127.0.0.1:3000/livez
curl -fsS http://127.0.0.1:3000/health | jq
# 后者会返回 build.version / build.commit / build.build_time，能直接对账
```

## 4. 升级流程（零停机）

API 是无状态的，多实例 + LB 时可以做滚动升级；单机部署则会有几秒的接管窗口。

```sh
# 单机最小升级（接受几秒停机）：
sudo systemctl start go-skeleton-migrate.service
sudo install -m 0755 -o go-skeleton -g go-skeleton bin/api     /opt/go-skeleton/bin/api
sudo install -m 0755 -o go-skeleton -g go-skeleton bin/worker  /opt/go-skeleton/bin/worker
sudo systemctl restart go-skeleton-api.service
sudo systemctl restart go-skeleton-worker.service

# 验证版本对账：
curl -fsS http://127.0.0.1:3000/health | jq '.build'
/opt/go-skeleton/bin/api -version
```

多实例 + LB 时的标准做法：

1. `make release` 出新版本 tarball
2. 在 stage 主机上做完整升级 + 烟雾测试
3. **先跑一次 migration**（任一台执行即可，不必每台）：`go-skeleton-migrate.service`
4. 生产机器逐台升级：摘流量 → 装新二进制 → `systemctl restart` → 健康检查通过 → 加回流量
5. 全量更新完后整体跑端到端校验

> **迁移与代码部署的时序（重要）**：migration 在第一台执行后 schema 就全局生效，
> 但其余实例此刻还跑旧代码——会短暂出现"新 schema + 旧代码"。因此**迁移必须对
> 旧代码向后兼容**（加列、加表、加索引这类"只增不破坏"的变更天然安全）。破坏性
> 变更（删列 / 改类型 / 重命名）不要一步到位，走 **expand-contract 两阶段发布**：
>
> 1. **expand**：先加新列、双写新旧字段、部署能读写两者的代码；
> 2. **contract**：等所有实例都在新代码上、确认旧字段不再被读后，下一个版本再发
>    删旧列的迁移。
>
> 这样任意时刻"当前 schema"都能被"当前在线的任意代码版本"安全访问，滚动升级和
> 回滚都不会因 schema 漂移炸掉。
>
> 并发安全：`cmd/migrate` 用 Postgres advisory lock（goose session locker），多台
> 同时跑也只有一个真正执行、其余阻塞等待，不会竞态——但仍建议按上面"任一台跑一次"
> 的流程,把迁移当成独立步骤而非每台都跑。

## 5. 回滚

### 5.1 回滚代码（二进制）

```sh
# 假设旧版本二进制保留在 /opt/go-skeleton/bin/api.previous
sudo cp /opt/go-skeleton/bin/api.previous /opt/go-skeleton/bin/api
sudo systemctl restart go-skeleton-api.service

# 或者从 GitHub Releases 重新下载旧版本走第 2/3 步
```

部署脚本建议每次升级前 `cp api api.previous` 留一份，回滚单条命令搞定。

### 5.2 回滚 schema（迁移）

**首选：不回滚 schema。** 如果迁移遵循了 §4 的 expand-contract 原则（只增不破坏），
旧代码能直接跑在新 schema 上——回滚二进制即可，**不用动数据库**。这是最安全的路径。

只有当新版本带了**破坏性迁移**、且必须把 schema 也退回时，才用 goose 回滚：

```sh
# 回滚最近一版迁移（执行该版本的 -- +goose Down 段）
sudo -u go-skeleton /opt/go-skeleton/bin/migrate -cmd down
# 看当前版本确认已退回
sudo -u go-skeleton /opt/go-skeleton/bin/migrate -cmd status
```

> ⚠️ **schema 回滚有数据风险**：`down` 执行的是迁移文件里的 `-- +goose Down` 段。
> 如果 Up 是"删列"、Down 是"重新加列"，回滚**不会恢复列里原来的数据**（已经随
> Up 丢了）。所以：
> - 写迁移时认真写 Down，但别指望它能还原数据；
> - 真正的数据安全靠**升级前对数据库做备份**（如 `pg_dump`），而不是靠 `down`；
> - 生产环境优先用 expand-contract 避免回滚 schema，把 `down` 当应急手段而非常规操作。

## 6. 日常运维

```sh
# 查看日志（实时）
sudo journalctl -u go-skeleton-api.service -f
sudo journalctl -u go-skeleton-worker.service -f

# 历史日志按时间过滤
sudo journalctl -u go-skeleton-api.service --since "1 hour ago"
sudo journalctl -u go-skeleton-api.service --since "2026-05-18 09:00" --until "2026-05-18 10:00"

# 按 trace_id 找请求（trace_id 写在结构化日志的 trace_id 字段里）
sudo journalctl -u go-skeleton-api.service | jq 'select(.trace_id=="abc-123")'

# 重启 / 停止
sudo systemctl restart go-skeleton-api.service
sudo systemctl stop    go-skeleton-worker.service

# 看资源使用
systemd-cgtop -m | grep go-skeleton
```

## 7. 排错 cheat sheet

| 现象 | 诊断命令 |
| --- | --- |
| `systemctl start` 失败 | `journalctl -u go-skeleton-api.service -n 50 --no-pager` |
| 启动后立刻退出 | 99% 是 `.env` 配置错；改完 `systemctl restart` |
| `/health` 返回 503 | `curl http://127.0.0.1:3000/health \| jq` 看哪个 check 是 unavailable |
| 想确认跑的是哪个版本 | `/opt/go-skeleton/bin/api -version` 或 `curl /health \| jq .build` |
| 配置文件改了不生效 | systemd unit 用 `EnvironmentFile` 静态读，必须 `systemctl restart` |
| Worker 任务停了 | 检查 Redis 是否可达；`journalctl -u go-skeleton-worker.service` 看 asynq 日志 |

## 8. 安全注意

- `/etc/go-skeleton/.env` mode 应为 `0640`（root 写、go-skeleton 读），**不要** `0644`
- systemd unit 已开启 `ProtectSystem=strict / PrivateTmp / NoNewPrivileges` 等加固选项，不要随便关
- 用 `firewalld` / `iptables` 把 `:3000` 限制只接 LB；`/livez`、`/health`、`/openapi.json` 不要直接暴露公网
- `JWT_SECRET` 与 Postgres / Redis 凭据**只**放 `.env`，不要扔进环境变量被 `ps` / `/proc` 看到

## 9. systemd 运行时

### API unit

- `Type=notify` + `WatchdogSec=30s`：API 进程通过 sd_notify 发 `READY=1`（启动完成）和
  周期 `WATCHDOG=1`（心跳）。`WATCHDOG_INTERVAL=10s` 是心跳周期，约为 WatchdogSec 的 1/3。
  systemd 在 30s 内未收到心跳即按 `Restart=on-failure` 重启。
- `LimitNOFILE=65535`：HTTP server + DB/Redis 连接池 + asynq client 累积文件描述符；
  默认 1024 在中等并发下吃紧。

### Worker unit

- `Type=notify` + `WatchdogSec=60s`：worker 同样发 READY/WATCHDOG。窗口比 API 宽
  （60s vs 30s）——worker 单条 task 可能合法阻塞在长 DB / 外部 I/O 上；误杀进行中的
  task 比误杀无状态 HTTP 请求代价更高。心跳周期复用 `WATCHDOG_INTERVAL=10s`。
- 配 `LimitNOFILE=65535` 同 API 理由。
- watchdog 不能替代业务监控：仍然要看 Asynqmon 队列堆积、ErrorHandler 日志。watchdog
  只兜底"进程卡死"，识别不了"进程在跑但任务一直失败"。

### Migrate unit

迁移是一次性进程，不需要 watchdog，保持 `Type=simple`。

更多上线前检查见 [README "Production Checklist"](../README.md#production-checklist)。
