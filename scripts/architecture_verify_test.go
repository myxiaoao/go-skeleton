// architecture_verify_test.go 覆盖 architecture-verify.go 的分层依赖规则:
// 规则 1 service 不 import gin、规则 2 pgx / sqlcdb 限定到 repository /
// bootstrap / pkg/database、规则 5 全仓禁止 gorm、规则 6 task payload 必须
// 匿名内嵌 Header。
package scripts

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitectureVerify_GinInService(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 规则 1 的命中文件：service 包不能 import gin。
	writeFile(t, filepath.Join(dir, "internal", "service", "x.go"), `package service

import "github.com/gin-gonic/gin"

var _ = gin.Default
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when service imports gin\n%s", out)
	}
	if !strings.Contains(out, "rule 1") || !strings.Contains(out, "github.com/gin-gonic/gin") {
		t.Errorf("expected rule 1 + gin in diagnostic, got:\n%s", out)
	}
	if !strings.Contains(out, "internal/service/x.go") {
		t.Errorf("expected service/x.go in diagnostic, got:\n%s", out)
	}
}

func TestArchitectureVerify_PgxOutsideAllowList(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 规则 2 的命中：handler 包不能 import pgx 或 sqlc 生成包。
	writeFile(t, filepath.Join(dir, "internal", "handler", "x.go"), `package handler

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"go-skeleton/internal/repository/sqlcdb"
)

var (
	_ *pgxpool.Pool
	_ sqlcdb.DBTX
)
`)
	// 允许列表：repository 与 pkg/database。
	writeFile(t, filepath.Join(dir, "internal", "repository", "ok.go"), `package repository

import "github.com/jackc/pgx/v5"

var _ pgx.Tx
`)
	writeFile(t, filepath.Join(dir, "pkg", "database", "ok.go"), `package database

import "github.com/jackc/pgx/v5/pgxpool"

var _ *pgxpool.Pool
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when handler imports pgx\n%s", out)
	}
	if !strings.Contains(out, "rule 2") || !strings.Contains(out, "internal/handler/x.go") {
		t.Errorf("expected rule 2 + handler/x.go, got:\n%s", out)
	}
	if !strings.Contains(out, "go-skeleton/internal/repository/sqlcdb") {
		t.Errorf("expected sqlcdb import flagged, got:\n%s", out)
	}
	for _, ok := range []string{"internal/repository/ok.go", "pkg/database/ok.go"} {
		if strings.Contains(out, ok) {
			t.Errorf("%s is in allow list, should not be flagged:\n%s", ok, out)
		}
	}
}

func TestArchitectureVerify_GormForbidden(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 规则 5：gorm 已彻底移除，即使 repository 也不允许 import。
	writeFile(t, filepath.Join(dir, "internal", "repository", "x.go"), `package repository

import "gorm.io/gorm"

var _ = gorm.DB{}
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when anything imports gorm\n%s", out)
	}
	if !strings.Contains(out, "rule 5") || !strings.Contains(out, "internal/repository/x.go") {
		t.Errorf("expected rule 5 + repository/x.go, got:\n%s", out)
	}
}

func TestArchitectureVerify_PayloadHeaderEmbedded(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 正例：Header 匿名内嵌在首位 → 规则 6 通过。
	writeFile(t, filepath.Join(dir, "internal", "task", "example.go"), `package task

type Header struct {
	Version int
	TraceID string
}

type ExamplePayload struct {
	Header
	Name string
}
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code != 0 {
		t.Fatalf("architecture-verify should pass when Header is embedded first\n%s", out)
	}
	if !strings.Contains(out, "clean") {
		t.Errorf("expected 'clean' in success output, got:\n%s", out)
	}
}

func TestArchitectureVerify_PayloadMissingHeader(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 反例：Payload 完全没有内嵌 Header。
	writeFile(t, filepath.Join(dir, "internal", "task", "bad.go"), `package task

type BadPayload struct {
	Name string
}
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when Payload missing embedded Header\n%s", out)
	}
	if !strings.Contains(out, "rule 6") {
		t.Errorf("expected rule 6 in diagnostic, got:\n%s", out)
	}
	if !strings.Contains(out, "internal/task/bad.go") {
		t.Errorf("expected internal/task/bad.go in diagnostic, got:\n%s", out)
	}
}

func TestArchitectureVerify_PayloadHeaderNotFirst(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 反例：Header 内嵌了，但不是首字段。
	writeFile(t, filepath.Join(dir, "internal", "task", "bad.go"), `package task

type Header struct {
	Version int
}

type BadPayload struct {
	Name string
	Header
}
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when Header is not the first field\n%s", out)
	}
	if !strings.Contains(out, "rule 6") {
		t.Errorf("expected rule 6 in diagnostic, got:\n%s", out)
	}
	if !strings.Contains(out, "internal/task/bad.go") {
		t.Errorf("expected internal/task/bad.go in diagnostic, got:\n%s", out)
	}
}

// TestArchitectureVerify_PayloadAliases 覆盖别名 / 基于其他类型定义的 Payload：
// 规则 6 要顺着同包名字解析到最终 struct 再判定，引用其他包的类型一律报错。
func TestArchitectureVerify_PayloadAliases(t *testing.T) {
	const header = "package task\n\ntype Header struct {\n\tVersion int\n}\n\n"
	cases := []struct {
		name     string
		body     string
		wantFail bool
	}{
		{
			name:     "别名指向内嵌 Header 的 struct",
			body:     "type base struct {\n\tHeader\n\tName string\n}\n\ntype GoodPayload = base\n",
			wantFail: false,
		},
		{
			name:     "定义基于内嵌 Header 的 struct（多级）",
			body:     "type base struct {\n\tHeader\n}\n\ntype mid base\n\ntype GoodPayload mid\n",
			wantFail: false,
		},
		{
			name:     "别名指向缺 Header 的 struct",
			body:     "type base struct {\n\tName string\n}\n\ntype BadPayload = base\n",
			wantFail: true,
		},
		{
			name:     "引用其他包的类型",
			body:     "import \"time\"\n\ntype BadPayload = time.Time\n",
			wantFail: true,
		},
		{
			name:     "非 struct 形态跳过",
			body:     "type RawPayload []byte\n",
			wantFail: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			initRepo(t, dir)
			body := c.body
			src := header + body
			if strings.HasPrefix(body, "import") {
				// import 必须紧跟 package 声明。
				src = "package task\n\n" + body + "\ntype Header struct {\n\tVersion int\n}\n"
			}
			writeFile(t, filepath.Join(dir, "internal", "task", "p.go"), src)

			code, out := runScript(t, dir, "architecture-verify.go")
			if c.wantFail {
				if code == 0 || !strings.Contains(out, "rule 6") || !strings.Contains(out, "internal/task/p.go") {
					t.Fatalf("expected rule 6 violation for internal/task/p.go, exit=%d\n%s", code, out)
				}
				return
			}
			if code != 0 {
				t.Fatalf("expected clean, exit=%d\n%s", code, out)
			}
		})
	}
}

func TestArchitectureVerify_SkipsClaudeWorktrees(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// .claude/worktrees/ 是本地 agent 起的嵌套 git worktree，不属于本仓库
	// 源码——walkGoFiles 应当整目录跳过，规则 2/5 都不该扫进去误报。
	writeFile(t, filepath.Join(dir, ".claude", "worktrees", "x", "internal", "handler", "x.go"), `package handler

import "gorm.io/gorm"

var _ = gorm.DB{}
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code != 0 {
		t.Fatalf("architecture-verify should ignore .claude/worktrees, got exit=%d\n%s", code, out)
	}
	if strings.Contains(out, ".claude") {
		t.Errorf("expected no mention of .claude path, got:\n%s", out)
	}
}

func TestArchitectureVerify_Clean(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 空仓库：service / repository / pkg 都没文件，所有规则都应通过。
	code, out := runScript(t, dir, "architecture-verify.go")
	if code != 0 {
		t.Fatalf("architecture-verify exit=%d on empty repo, expected 0\n%s", code, out)
	}
	if !strings.Contains(out, "clean") {
		t.Errorf("expected 'clean' in success output, got:\n%s", out)
	}
}
