//go:build ignore

// docs-verify 保证 AGENTS.md 和 CLAUDE.md 在那些"应该一直保持一致"的
// `## <heading>` 段落上不漂移。两份文件是给不同 AI 编码助手并行维护的，
// 漏改一份是最常见的悄无声息腐化。
//
// 入口：
//
//	go run scripts/docs-verify.go
//	go run scripts/docs-verify.go CLAUDE.md AGENTS.md     # 显式指定路径
//	make docs-verify                                       # 推荐
//
// 与旧 bash/awk 版的语义差异：
//   - 解析走"逐行扫描 H2"，标准 Markdown 行首 `## ` 才算 heading；
//     避免 awk 在代码块内的 `## ` 误命中（旧版没这个保护）。
//   - 段落对比走 strings.Compare：相等 ✓，不等输出 unified-style diff
//     头几行让人定位（不是 GNU diff 完整输出，但够用来看错位）。
//   - 校验项（sharedSections）数据驱动，加新段改一行。
//
// 不属于任何包，//go:build ignore 让 go build/test 跳过它。
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// sharedSections 是两份文件应当完全一致的 H2 标题集合。改这里同步更新
// CLAUDE.md / AGENTS.md 的对应段。新增段时往这里加一行。
//
// 顺序无关——比对按 set 做。这里写成 slice 便于 diff review 时看清楚清单。
var sharedSections = []string{
	"分层规则：handler → service → repository",
	"依赖装配（手写 DI）",
	"统一响应协议",
	"i18n",
	"JWT 鉴权",
	"异步队列",
	"context 传递（硬约束）",
	"环境变量",
	"审计日志",
	"pkg/ 边界",
	"AI 助手提示（最高频违反，每次进项目先扫这段）",
	"写代码时常犯的错（已知会触发返工）",
	"API 契约：OpenAPI 3.1",
	"验证命令",
	"测试约定",
	"Git Workflow",
}

func main() {
	claude := "CLAUDE.md"
	agents := "AGENTS.md"
	if len(os.Args) >= 3 {
		claude = os.Args[1]
		agents = os.Args[2]
	}

	for _, p := range []string{claude, agents} {
		if _, err := os.Stat(p); err != nil {
			fatal(fmt.Errorf("%s not found", p))
		}
	}

	claudeMap, err := extractSections(claude)
	if err != nil {
		fatal(err)
	}
	agentsMap, err := extractSections(agents)
	if err != nil {
		fatal(err)
	}

	mismatched := 0
	for _, sec := range sharedSections {
		cb, hasC := claudeMap[sec]
		ab, hasA := agentsMap[sec]
		if !hasC {
			fmt.Fprintf(os.Stderr, "docs-verify: section [%s] missing in %s\n", sec, claude)
			mismatched++
			continue
		}
		if !hasA {
			fmt.Fprintf(os.Stderr, "docs-verify: section [%s] missing in %s\n", sec, agents)
			mismatched++
			continue
		}
		if cb == ab {
			continue
		}
		fmt.Fprintf(os.Stderr, "docs-verify: section [%s] differs between %s and %s\n", sec, claude, agents)
		printDiff(cb, ab)
		fmt.Fprintln(os.Stderr)
		mismatched++
	}

	if mismatched > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "docs-verify: AGENTS.md and CLAUDE.md are out of sync.")
		fmt.Fprintln(os.Stderr, "             Apply the same change to both files and re-run.")
		os.Exit(1)
	}

	fmt.Println("docs-verify: AGENTS.md and CLAUDE.md shared sections in sync.")
}

// extractSections 把 path 切成 `## <heading>` → body 的 map。body 是
// 该 heading 之后到下一个 `## ` 之间的所有行（含末尾换行），与旧 awk
// 版语义一致。
//
// "行首 `## `" 才算 heading：代码块（``` 围起来的段）里的 `## ` 不算。
// 这是相对旧版的收紧——awk 版没维护 code-fence 状态。
func extractSections(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sections := map[string]string{}
	var cur string
	var buf strings.Builder
	inCodeFence := false

	flush := func() {
		if cur != "" {
			sections[cur] = buf.String()
		}
		buf.Reset()
	}

	sc := bufio.NewScanner(f)
	// 默认 Buffer 上限 64KB，CLAUDE.md/AGENTS.md 单行不会爆，但提升一档防意外。
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()

		// 维护代码块状态：行首 ``` 或 ~~~ 进出。不严格识别 info string，
		// 实际文档里都是 ```sh 这种形态。
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCodeFence = !inCodeFence
			if cur != "" {
				buf.WriteString(line)
				buf.WriteByte('\n')
			}
			continue
		}

		if !inCodeFence && strings.HasPrefix(line, "## ") {
			flush()
			cur = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}

		if cur != "" {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	return sections, nil
}

// printDiff 给两段 body 输出"前 20 个不同行"的简版 diff。不引第三方
// diff 库——足够定位"哪几行错位了"即可。
func printDiff(a, b string) {
	aLines := strings.Split(a, "\n")
	bLines := strings.Split(b, "\n")
	maxLen := max(len(aLines), len(bLines))
	shown := 0
	const limit = 20
	for i := 0; i < maxLen && shown < limit; i++ {
		var la, lb string
		if i < len(aLines) {
			la = aLines[i]
		}
		if i < len(bLines) {
			lb = bLines[i]
		}
		if la == lb {
			continue
		}
		if i < len(aLines) {
			fmt.Fprintf(os.Stderr, "  - %s\n", la)
		}
		if i < len(bLines) {
			fmt.Fprintf(os.Stderr, "  + %s\n", lb)
		}
		shown++
	}
	if shown >= limit {
		fmt.Fprintln(os.Stderr, "  ... (truncated; fix the first few and re-run)")
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "docs-verify:", err)
	os.Exit(1)
}
