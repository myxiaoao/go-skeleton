#!/usr/bin/env bash
# scripts/architecture-verify.sh
#
# 把 CLAUDE.md / AGENTS.md "分层规则"段落里的 import 边界从"靠人/AI 记
# 住"变成"机器拦截"。CI / make verify 会调它；失败时输出违规文件 + 行号，
# 直接定位。
#
# 检查项（与 docs 同步，加新规则时记得同时更新规则文档）：
#
# 1. service / repository / model / task / worker / taskqueue 包不能 import
#    "github.com/gin-gonic/gin"。理由：service 也要被 worker 消费，绑 gin
#    会让 worker 跑不通；下游层更不该接触 transport 框架。
#
# 2. gorm.io/gorm 只允许出现在 internal/repository/、internal/model/、
#    internal/bootstrap/、pkg/database/ 这几条已确定的"持久化装配链"上。
#    handler / service / middleware 等业务层禁止 import。
#
# 3. pkg/ 下任何文件禁止 import go-skeleton/internal/*。pkg/ 是通用工具，
#    理论上可被其他项目复用；反向依赖会让 pkg 失去这个属性。
#
# 4. internal/service/ 和 internal/handler/ 的运行时代码（非 _test.go）禁
#    止 context.Background()。这两层一定有外部传入的 ctx（HTTP request /
#    asynq task），用 Background 会丢 trace_id / 超时。bootstrap / server.go
#    起后台 goroutine 是合法例外，不在本规则范围。
#
# 行号通过 grep -n 输出，便于编辑器跳转。测试文件统一豁免（合法用例）。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

violations=0

# 公共 helper：grep 出违规 → 排除 _test.go → 打印 → 累计 violations 计数。
# 用法：check_imports <规则编号> <说明> <grep 模式> <搜索路径...>
check_imports() {
  local rule="$1"
  local desc="$2"
  local pattern="$3"
  shift 3
  local hits
  if ! hits=$(grep -rn --include='*.go' -- "$pattern" "$@" 2>/dev/null); then
    return 0
  fi
  # 排除 _test.go：测试文件里出现 gorm / gin 都是合法的。
  hits=$(printf '%s\n' "$hits" | grep -v '_test\.go:' || true)
  if [ -z "$hits" ]; then
    return 0
  fi
  echo "architecture-verify: [rule $rule] $desc" >&2
  printf '%s\n' "$hits" >&2
  echo >&2
  violations=$((violations + 1))
}

# 规则 1：下游层禁止 import gin。
# 不写 -F：grep 默认就是 BRE，"gin" 不是元字符。
for pkg in internal/service internal/repository internal/model \
           internal/task internal/worker internal/taskqueue; do
  if [ -d "$pkg" ]; then
    check_imports 1 \
      "$pkg 禁止 import github.com/gin-gonic/gin" \
      '"github.com/gin-gonic/gin"' \
      "$pkg"
  fi
done

# 规则 2：gorm 限制在 repository / model / bootstrap / pkg/database。
# 用 git ls-files 列所有 .go 然后排除允许目录，比 grep --exclude 更可靠。
gorm_violators=$(grep -rln --include='*.go' -- '"gorm.io/gorm"' . 2>/dev/null | \
  grep -v '^\./internal/repository/' | \
  grep -v '^\./internal/model/' | \
  grep -v '^\./internal/bootstrap/' | \
  grep -v '^\./pkg/database/' | \
  grep -v '_test\.go$' || true)
if [ -n "$gorm_violators" ]; then
  echo "architecture-verify: [rule 2] gorm.io/gorm 仅允许 repository / model / bootstrap / pkg/database 使用" >&2
  while IFS= read -r f; do
    grep -n '"gorm.io/gorm"' "$f" | sed "s|^|$f:|" >&2
  done <<<"$gorm_violators"
  echo >&2
  violations=$((violations + 1))
fi

# 规则 3：pkg/ 禁止反向依赖 internal/。
check_imports 3 \
  "pkg/ 禁止 import go-skeleton/internal/*" \
  '"go-skeleton/internal' \
  pkg

# 规则 4：service / handler 运行时代码禁止 context.Background()。
for pkg in internal/service internal/handler; do
  if [ -d "$pkg" ]; then
    check_imports 4 \
      "$pkg 运行时代码禁止 context.Background()（应当透传外部 ctx）" \
      'context\.Background()' \
      "$pkg"
  fi
done

if [ "$violations" -gt 0 ]; then
  echo "architecture-verify: $violations rule(s) violated. 详见上面的文件:行号清单。" >&2
  exit 1
fi

echo "architecture-verify: 4 import / context rules clean."
