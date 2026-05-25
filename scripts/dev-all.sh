#!/usr/bin/env bash
# scripts/dev-all.sh — 一条命令起完整本地三进程：依赖 → 迁移 → API + Worker。
#
# Usage:
#   make dev-all          # 推荐入口
#   ./scripts/dev-all.sh  # 直接调
#
# 行为：
#   1. 探活 Postgres + Redis（dev-deps-check 等价逻辑，复用 .env 解析）。
#      不可达时不自动起 docker——caller 自行 `make dev-up`，避免脚本越权
#      改本机环境。
#   2. 跑 `go run ./cmd/migrate -cmd up`，迁移完才放行后续进程，避免 API
#      启动期撞未建好的表。
#   3. 并发起 API + Worker，前台跟随两路日志（前缀 [api] / [worker]
#      区分），Ctrl-C 优雅停：SIGINT/SIGTERM 触发 trap，向两个 PID
#      发 SIGTERM，等最多 GRACEFUL_SHUTDOWN_TIMEOUT 秒再 SIGKILL 兜底。
#
# 不做的事：
#   - 不起 docker compose：本地有 brew 装的 Postgres / Redis 时不应该
#     被 docker 抢端口。要 docker 自己先 `make dev-up`。
#   - 不带文件热重载：那是 `make watch` 的事，dev-all 是"端到端跑一遍"。
#
# 进程模型：
#   主脚本 fork 两个子进程（cmd/api、cmd/worker），各自把 stdout/stderr
#   通过 awk 行缓冲加前缀后写主进程的 stdout。监控用 polling 写法，兼
#   容 macOS 自带的 bash 3.2（没有 `wait -n`）。

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# 优雅停超时：超过这个时间还没退就 SIGKILL。
GRACEFUL_SHUTDOWN_TIMEOUT="${GRACEFUL_SHUTDOWN_TIMEOUT:-15}"

API_PID=""
WORKER_PID=""
CLEANUP_DONE=0

cleanup() {
  # trap 可能被多次触发（INT 之后 EXIT 又进一次）。CLEANUP_DONE 哨兵让
  # 第二次直接跳过，避免对已 wait 完的 PID 反复发信号 / 打多余日志。
  if [ "$CLEANUP_DONE" = "1" ]; then
    return
  fi
  CLEANUP_DONE=1

  echo
  echo "[dev-all] shutting down..."
  for pid in "$API_PID" "$WORKER_PID"; do
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      echo "[dev-all] SIGTERM -> $pid"
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done

  # 等子进程优雅退；超时后 SIGKILL 兜底。
  waited=0
  while [ "$waited" -lt "$GRACEFUL_SHUTDOWN_TIMEOUT" ]; do
    alive=0
    for pid in "$API_PID" "$WORKER_PID"; do
      if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        alive=1
      fi
    done
    [ "$alive" -eq 0 ] && break
    sleep 1
    waited=$((waited + 1))
  done

  for pid in "$API_PID" "$WORKER_PID"; do
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      echo "[dev-all] SIGKILL -> $pid (did not stop within ${GRACEFUL_SHUTDOWN_TIMEOUT}s)"
      kill -KILL "$pid" 2>/dev/null || true
    fi
  done
  echo "[dev-all] stopped."
}
trap cleanup INT TERM EXIT

# ---- 1. 依赖探活 ----
echo "[dev-all] checking deps (Postgres + Redis)..."
if ! make --no-print-directory dev-deps-check; then
  cat >&2 <<'EOF'

[dev-all] deps unreachable. Choose one:
  - docker:  make dev-up
  - brew:    brew services start postgresql@17 && brew services start redis
  - manual:  start Postgres / Redis yourself, then re-run.

EOF
  exit 1
fi

# ---- 2. 迁移 ----
echo "[dev-all] running migrations..."
go run ./cmd/migrate -cmd up

# ---- 3. 并发起 API + Worker，加前缀输出 ----
#
# 用 awk 给每行加前缀（"[api]    " / "[worker] "）。awk 比 sed 跨平台
# 省心：BSD sed 行缓冲是 -l，GNU sed 是 -u，写哪个都会在另一侧崩。awk
# 走 fflush(stdout) 显式刷，所有实现都一致。
echo "[dev-all] starting api + worker (Ctrl-C to stop both)..."

go run ./cmd/api 2>&1 \
  | awk '{ print "[api]    " $0; fflush(stdout) }' &
API_PID=$!

go run ./cmd/worker 2>&1 \
  | awk '{ print "[worker] " $0; fflush(stdout) }' &
WORKER_PID=$!

# 监控两个 PID，任一退出就触发 cleanup。用 polling 写法因为 macOS 自带
# bash 3.2 不支持 `wait -n`。0.5s 周期足够：开发时挂掉到收到提示之间的
# 延迟可以接受，CPU 占用可忽略。
exit_code=0
while true; do
  if [ -n "$API_PID" ] && ! kill -0 "$API_PID" 2>/dev/null; then
    wait "$API_PID" 2>/dev/null
    exit_code=$?
    API_PID=""
    echo "[dev-all] api exited (code=$exit_code), stopping worker..."
    break
  fi
  if [ -n "$WORKER_PID" ] && ! kill -0 "$WORKER_PID" 2>/dev/null; then
    wait "$WORKER_PID" 2>/dev/null
    exit_code=$?
    WORKER_PID=""
    echo "[dev-all] worker exited (code=$exit_code), stopping api..."
    break
  fi
  sleep 0.5
done

# trap 会收尾另一个 PID + 打印 stopped。直接退出，把已退出那个的 exit
# code 透传出去——CI / 监控看到非零就知道有进程挂了。
exit "$exit_code"
