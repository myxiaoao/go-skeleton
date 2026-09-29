// docs_verify_test.go 覆盖 docs-verify.go 的回归：CLAUDE.md 必须 @AGENTS.md
// 导入且不重复 AGENTS.md 的 `## ` 段；各文档 `make verify   # ...` 清单与
// Makefile verify 目标的 _verify-step 序列逐项一致。
package scripts

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"
)

const docsVerifyMakefile = "verify: ## 一站式校验（fmt-verify + vet + test）\n" +
	"\t@$(MAKE) --no-print-directory _verify-step STEP=fmt-verify\n" +
	"\t@$(MAKE) --no-print-directory _verify-step STEP=vet\n" +
	"\t@$(MAKE) --no-print-directory _verify-step STEP=test\n" +
	"\t@printf 'verify OK'\n" +
	"\n" +
	"_verify-step:\n" +
	"\t@$(MAKE) $(STEP)\n"

// writeDocsVerifyFixture 写一个能让 docs-verify 通过的迷你仓库；overrides
// 按相对路径覆盖单个文件，用来构造各失败场景。
func writeDocsVerifyFixture(t *testing.T, overrides map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)

	files := map[string]string{
		"Makefile":            docsVerifyMakefile,
		"CLAUDE.md":           "# 入口\n\n@AGENTS.md\n\n## Claude Code 专属补充\n\n- 插件说明\n",
		"AGENTS.md":           "# 规则\n\n## 分层规则\n\n```sh\n## 代码块里的不算标题\nmake verify   # fmt-verify + vet + test（每步打横幅）\n```\n",
		"README.md":           "```sh\nmake verify   # fmt-verify + vet + test\nmake oapi-verify   # make verify 会调用\n```\n",
		"README_en.md":        "```sh\nmake verify   # fmt-verify + vet + test\n```\n",
		"docs/development.md": "```sh\nmake verify              # fmt-verify + vet + test（每步打横幅）\n```\n",
		"docs/runbook.md":     "```sh\nmake verify        # 项目级一站式\n```\n",
	}
	maps.Copy(files, overrides)
	for name, content := range files {
		writeFile(t, filepath.Join(dir, name), content)
	}
	return dir
}

func TestDocsVerify_HappyPath(t *testing.T) {
	dir := writeDocsVerifyFixture(t, nil)

	code, out := runScript(t, dir, "docs-verify.go")
	if code != 0 {
		t.Fatalf("docs-verify exit=%d, expected 0\n%s", code, out)
	}
	// AGENTS / README / README_en / development 各一条；runbook 那行没有 + 清单，不计入。
	if !strings.Contains(out, "4 verify list(s) match Makefile (3 steps)") {
		t.Errorf("unexpected success output:\n%s", out)
	}
}

// CLAUDE.md 代码块里出现与 AGENTS.md 同名的 `## ` 行（比如示范写法）不算重复段。
func TestDocsVerify_FencedHeadingInClaudeIgnored(t *testing.T) {
	dir := writeDocsVerifyFixture(t, map[string]string{
		"CLAUDE.md": "# 入口\n\n@AGENTS.md\n\n```md\n## 分层规则\n```\n",
	})

	code, out := runScript(t, dir, "docs-verify.go")
	if code != 0 {
		t.Fatalf("docs-verify exit=%d, expected 0 (fenced heading must be ignored)\n%s", code, out)
	}
}

func TestDocsVerify_Failures(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		wants     []string
	}{
		{
			name:      "missing import line",
			overrides: map[string]string{"CLAUDE.md": "# 入口\n\n见 AGENTS.md\n"},
			wants:     []string{"CLAUDE.md must contain a standalone `@AGENTS.md` line"},
		},
		{
			name:      "duplicated rule section",
			overrides: map[string]string{"CLAUDE.md": "# 入口\n\n@AGENTS.md\n\n## 分层规则\n\n复制回来的正文\n"},
			wants:     []string{"CLAUDE.md:5: section [## 分层规则] duplicates AGENTS.md"},
		},
		{
			name:      "import line only inside code fence",
			overrides: map[string]string{"CLAUDE.md": "# 入口\n\n```md\n@AGENTS.md\n```\n"},
			wants:     []string{"CLAUDE.md must contain a standalone `@AGENTS.md` line outside code fences"},
		},
		{
			name:      "doc list missing step",
			overrides: map[string]string{"README_en.md": "text\n\n    make verify   # fmt-verify + test\n"},
			wants: []string{
				"README_en.md:3: verify step list drifted from Makefile",
				"got:      fmt-verify + test",
				"expected: fmt-verify + vet + test",
			},
		},
		{
			name:      "doc list wrong order",
			overrides: map[string]string{"docs/development.md": "make verify   # vet + fmt-verify + test（说明）\n"},
			wants:     []string{"docs/development.md:1: verify step list drifted", "got:      vet + fmt-verify + test"},
		},
		{
			name: "makefile help text drifted",
			overrides: map[string]string{"Makefile": strings.Replace(docsVerifyMakefile,
				"（fmt-verify + vet + test）", "（fmt + vet + test）", 1)},
			wants: []string{"Makefile:1: verify step list drifted", "got:      fmt + vet + test"},
		},
		{
			name: "makefile gained a step",
			overrides: map[string]string{"Makefile": strings.Replace(docsVerifyMakefile,
				"\t@printf", "\t@$(MAKE) --no-print-directory _verify-step STEP=lint\n\t@printf", 1)},
			wants: []string{"expected: fmt-verify + vet + test + lint", "AGENTS.md:7:", "README.md:2:"},
		},
		{
			name:      "no verify target",
			overrides: map[string]string{"Makefile": "build:\n\tgo build ./...\n"},
			wants:     []string{"`verify:` target not found"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeDocsVerifyFixture(t, tc.overrides)
			code, out := runScript(t, dir, "docs-verify.go")
			if code == 0 {
				t.Fatalf("docs-verify should fail\n%s", out)
			}
			for _, w := range tc.wants {
				if !strings.Contains(out, w) {
					t.Errorf("expected %q in output, got:\n%s", w, out)
				}
			}
		})
	}
}
