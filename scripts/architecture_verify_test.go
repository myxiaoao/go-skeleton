// architecture_verify_test.go 覆盖 architecture-verify.go 的分层依赖规则:
// 规则 1 service 不 import gin、规则 2 pgx / sqlcdb 限定到 repository /
// bootstrap / pkg/database、规则 5 全仓禁止 gorm。
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

	// 空仓库：service / repository / pkg 都没文件，5 条规则都应通过。
	code, out := runScript(t, dir, "architecture-verify.go")
	if code != 0 {
		t.Fatalf("architecture-verify exit=%d on empty repo, expected 0\n%s", code, out)
	}
	if !strings.Contains(out, "clean") {
		t.Errorf("expected 'clean' in success output, got:\n%s", out)
	}
}
