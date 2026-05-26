// architecture_verify_test.go 覆盖 architecture-verify.go 的分层依赖规则:
// 规则 1 service 不 import gin、规则 2 gorm 限定到 repository / model /
// bootstrap / pkg/database 子集。
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

func TestArchitectureVerify_GormOutsideAllowList(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 规则 2 的命中：handler 包不能 import gorm（只允许 repository / model /
	// bootstrap / pkg/database）。
	writeFile(t, filepath.Join(dir, "internal", "handler", "x.go"), `package handler

import "gorm.io/gorm"

var _ = gorm.DB{}
`)
	// 反例：repository 包 import gorm，不应被告警。
	writeFile(t, filepath.Join(dir, "internal", "repository", "ok.go"), `package repository

import "gorm.io/gorm"

var _ = gorm.DB{}
`)

	code, out := runScript(t, dir, "architecture-verify.go")
	if code == 0 {
		t.Fatalf("architecture-verify should fail when handler imports gorm\n%s", out)
	}
	if !strings.Contains(out, "rule 2") {
		t.Errorf("expected rule 2 in diagnostic, got:\n%s", out)
	}
	if !strings.Contains(out, "internal/handler/x.go") {
		t.Errorf("expected handler/x.go violation, got:\n%s", out)
	}
	if strings.Contains(out, "internal/repository/ok.go") {
		t.Errorf("repository/ok.go is in allow list, should not be flagged:\n%s", out)
	}
}

func TestArchitectureVerify_Clean(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// 空仓库：service / repository / pkg 都没文件，4 条规则都应通过。
	code, out := runScript(t, dir, "architecture-verify.go")
	if code != 0 {
		t.Fatalf("architecture-verify exit=%d on empty repo, expected 0\n%s", code, out)
	}
	if !strings.Contains(out, "clean") {
		t.Errorf("expected 'clean' in success output, got:\n%s", out)
	}
}
