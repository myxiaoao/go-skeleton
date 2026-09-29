//go:build ignore

// docs-verify 守两类"文档悄悄腐化"：
//
//  1. 规则文件单一来源：AGENTS.md 是全部 AI 编码助手共用的唯一规则文件，
//     CLAUDE.md 只通过单独一行 `@AGENTS.md`（Claude Code 导入语法）引用它。
//     校验 CLAUDE.md 含该导入行，且不出现任何与 AGENTS.md 同名的 `## ` 段
//     标题——防止有人又把规则正文复制回 CLAUDE.md，回到两份并行维护的老路。
//  2. verify 清单防漂移：解析 Makefile `verify:` 目标里 `_verify-step STEP=xxx`
//     的步骤序列（真相源），检查 verifyDocs 里每一行 `make verify   # a + b + ...`
//     的行尾注释列出的步骤与之完全一致（含顺序）；Makefile `verify` 的 `##`
//     帮助文案括号里的清单同样要一致。不一致时输出 文件:行 + 期望清单。
//
// 入口：
//
//	go run scripts/docs-verify.go
//	make docs-verify                                       # 推荐
//
// 只认行首 `## ` 为 H2 标题，代码块（``` / ~~~ 围起来的段）里的 `## ` 不算。
// 不属于任何包，//go:build ignore 让 go build/test 跳过它。
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

const (
	claudeFile   = "CLAUDE.md"
	agentsFile   = "AGENTS.md"
	makefileFile = "Makefile"
	importLine   = "@AGENTS.md"
)

// verifyDocs 是带 `make verify   # a + b + ...` 清单注释、需要与 Makefile
// 对齐的文档。新增写了该清单的文档时往这里加一行。
var verifyDocs = []string{
	"README.md",
	"README_en.md",
	"docs/development.md",
	"docs/runbook.md",
	agentsFile,
}

var stepRe = regexp.MustCompile(`_verify-step\s+STEP=(\S+)`)

func main() {
	problems := checkClaudeImport()

	steps, helpLine, helpSteps, err := parseMakefileVerify(makefileFile)
	if err != nil {
		fatal(err)
	}
	if len(steps) == 0 {
		fatal(fmt.Errorf("%s: no `_verify-step STEP=xxx` found under `verify:` target", makefileFile))
	}
	if helpSteps != nil && !slices.Equal(helpSteps, steps) {
		reportMismatch(makefileFile, helpLine, helpSteps, steps)
		problems++
	}

	lists := 0
	for _, doc := range verifyDocs {
		n, bad, err := checkVerifyLists(doc, steps)
		if err != nil {
			fatal(err)
		}
		lists += n
		problems += bad
	}

	if problems > 0 {
		fmt.Fprintf(os.Stderr, "\ndocs-verify: %d problem(s) found.\n", problems)
		os.Exit(1)
	}
	fmt.Printf("docs-verify: %s imports %s; %d verify list(s) match Makefile (%d steps).\n",
		claudeFile, agentsFile, lists, len(steps))
}

// checkClaudeImport 校验 CLAUDE.md 含单独一行 @AGENTS.md，且没有与 AGENTS.md
// 同名的 H2 段。返回发现的问题数。
func checkClaudeImport() int {
	claudeLines, err := readLines(claudeFile)
	if err != nil {
		fatal(err)
	}
	agentsLines, err := readLines(agentsFile)
	if err != nil {
		fatal(err)
	}

	problems := 0
	if !slices.ContainsFunc(claudeLines, func(l string) bool { return strings.TrimSpace(l) == importLine }) {
		fmt.Fprintf(os.Stderr, "docs-verify: %s must contain a standalone `%s` line (Claude Code import)\n", claudeFile, importLine)
		problems++
	}

	agentsHeadings := map[string]bool{}
	for _, h := range headings(agentsLines) {
		agentsHeadings[h.text] = true
	}
	for _, h := range headings(claudeLines) {
		if agentsHeadings[h.text] {
			fmt.Fprintf(os.Stderr, "docs-verify: %s:%d: section [## %s] duplicates %s; keep rules only in %s\n",
				claudeFile, h.line, h.text, agentsFile, agentsFile)
			problems++
		}
	}
	return problems
}

type heading struct {
	text string
	line int
}

// headings 返回代码块之外所有 `## ` 标题（去掉前缀）及其行号（1-based）。
func headings(lines []string) []heading {
	var out []heading
	inCodeFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCodeFence = !inCodeFence
			continue
		}
		if !inCodeFence && strings.HasPrefix(line, "## ") {
			out = append(out, heading{text: strings.TrimSpace(strings.TrimPrefix(line, "## ")), line: i + 1})
		}
	}
	return out
}

// parseMakefileVerify 读取 `verify:` 目标的 recipe（紧随其后、以 tab 开头的行），
// 按出现顺序收集 `_verify-step STEP=xxx`。同时解析 `verify: ## 描述（a + b）`
// 帮助文案里的清单；没有清单时 helpSteps 为 nil（不校验）。
func parseMakefileVerify(path string) (steps []string, helpLine int, helpSteps []string, err error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, 0, nil, err
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "verify:") {
			continue
		}
		if _, help, ok := strings.Cut(line, "##"); ok {
			_, inside := splitParen(help)
			helpLine, helpSteps = i+1, splitSteps(inside)
		}
		for _, r := range lines[i+1:] {
			if !strings.HasPrefix(r, "\t") {
				break
			}
			if m := stepRe.FindStringSubmatch(r); m != nil {
				steps = append(steps, m[1])
			}
		}
		return steps, helpLine, helpSteps, nil
	}
	return nil, 0, nil, fmt.Errorf("%s: `verify:` target not found", path)
}

// checkVerifyLists 扫 doc 里所有 `make verify   # a + b + ...` 行，逐行与 steps
// 比对。返回检查过的清单数与不一致数。
func checkVerifyLists(doc string, steps []string) (checked, bad int, err error) {
	lines, err := readLines(doc)
	if err != nil {
		return 0, 0, err
	}
	for i, line := range lines {
		cmd, comment, ok := strings.Cut(line, "#")
		if !ok || strings.TrimSpace(cmd) != "make verify" {
			continue
		}
		// 行尾注释形如 "a + b + c（说明）"：括号里是补充说明，不算步骤。
		before, _ := splitParen(comment)
		got := splitSteps(before)
		if got == nil {
			continue
		}
		checked++
		if !slices.Equal(got, steps) {
			reportMismatch(doc, i+1, got, steps)
			bad++
		}
	}
	return checked, bad, nil
}

// splitParen 把 "前缀（括号内）后缀" 切成括号前与括号内两段，全角 / 半角括号
// 等价；没有括号时 inside 为空。
func splitParen(s string) (before, inside string) {
	s = strings.NewReplacer("（", "(", "）", ")").Replace(s)
	before, rest, _ := strings.Cut(s, "(")
	inside, _, _ = strings.Cut(rest, ")")
	return before, inside
}

// splitSteps 把 "a + b + c" 按 + 切成步骤列表。不含 + 的文本不算清单，返回 nil。
func splitSteps(s string) []string {
	if !strings.Contains(s, "+") {
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(s, "+") {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func reportMismatch(file string, line int, got, want []string) {
	fmt.Fprintf(os.Stderr, "docs-verify: %s:%d: verify step list drifted from Makefile\n", file, line)
	fmt.Fprintf(os.Stderr, "  got:      %s\n", strings.Join(got, " + "))
	fmt.Fprintf(os.Stderr, "  expected: %s\n", strings.Join(want, " + "))
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	// 默认 Buffer 上限 64KB，文档单行不会爆，但提升一档防意外。
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "docs-verify:", err)
	os.Exit(1)
}
