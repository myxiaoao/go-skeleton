// scripts_test.go 给 scripts/ 下的 //go:build ignore 脚本加黑盒回归。
//
// 这些脚本本身是 main，没法被普通 import 调起来；用 t.TempDir 准备一个迷你
// "假仓库"，git init 后用 go run /abs/path/scripts/X.go 跑——脚本会把 cwd
// 当成仓库根来操作。比拆 testable 子包侵入小、比 mock fs 更贴真实行为。
//
// 范围只覆盖核心 parser / render 路径，不复刻所有边角 case：env-verify
// 的 helper 检测、architecture-verify 的规则 1 与 2、new-endpoint 的
// anchor 注入与"已存在拒绝覆盖"。出 bug 时这三类是最有可能漂的。
package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// containsTokens 把 src 拆成空白分隔的 token 序列，断言 tokens 按顺序出现
// 在某一行里（不要求紧邻，只要顺序）。用来跳过 gofmt 字段对齐留下的多
// 空格——直接 strings.Contains("Order *handler.OrderHandler") 会被对齐
// 后的 "Order   *handler.OrderHandler" 卡掉。
func containsTokens(src string, tokens ...string) bool {
	// 各 token 之间用 \s+ 连接，行内匹配（不跨行）。
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		parts[i] = regexp.QuoteMeta(t)
	}
	pattern := strings.Join(parts, `\s+`)
	return regexp.MustCompile(pattern).MatchString(src)
}

// thisDir 返回本测试文件所在目录的绝对路径（即 scripts/）。脚本路径由它拼出来。
func thisDir(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Dir(self)
}

// runScript 在 workdir 里跑 `go run <scripts/>/<script>` 加可选 args，返回
// 退出码 + 合并的 stdout/stderr。stderr 合到一起方便断言。
func runScript(t *testing.T, workdir, script string, args ...string) (int, string) {
	t.Helper()
	scriptPath := filepath.Join(thisDir(t), script)
	cmdArgs := append([]string{"run", scriptPath}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = workdir
	// 继承宿主 PATH / GOROOT 等关键 env；其余清掉避免污染。
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err == nil {
		return 0, out
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out
	}
	t.Fatalf("exec %s: %v\noutput:\n%s", script, err, out)
	return -1, out
}

// initRepo 在 dir 下 git init + 配 user.email / user.name + 一次空 commit，
// 让 git rev-parse --show-toplevel 能拿到 dir。脚本里 repoRoot() 全靠它。
func initRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet")
	run("config", "user.email", "ci@example.com")
	run("config", "user.name", "ci")
}

// writeFile 是 t.TempDir 子树的便利写入。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// env-verify

func TestEnvVerify_HappyPath(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	writeFile(t, filepath.Join(dir, "config", "config.go"), `package config

import "os"

func Load() {
	_ = os.Getenv("APP_ENV")
	_ = intEnv("HTTP_PORT", 3000)
}

func intEnv(key string, def int) int { _ = key; return def }
`)
	writeFile(t, filepath.Join(dir, ".env.example"), `APP_ENV=development
HTTP_PORT=3000
`)

	code, out := runScript(t, dir, "env-verify.go")
	if code != 0 {
		t.Fatalf("env-verify exit=%d, expected 0\n%s", code, out)
	}
	if !strings.Contains(out, "2 keys") {
		t.Errorf("expected '2 keys' in output, got:\n%s", out)
	}
}

func TestEnvVerify_MissingInExample(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	// config 读了 HTTP_PORT，模板里没列。脚本应在 stderr 指出 HTTP_PORT 缺失。
	writeFile(t, filepath.Join(dir, "config", "config.go"), `package config

import "os"

func Load() {
	_ = os.Getenv("HTTP_PORT")
}
`)
	writeFile(t, filepath.Join(dir, ".env.example"), `APP_ENV=development
`)

	code, out := runScript(t, dir, "env-verify.go")
	if code == 0 {
		t.Fatalf("env-verify should fail when .env.example missing keys\n%s", out)
	}
	if !strings.Contains(out, "HTTP_PORT") {
		t.Errorf("expected HTTP_PORT in diagnostic, got:\n%s", out)
	}
}

// 注释 / 字符串字面量里出现 KEY 字样不应被命中——AST 走 CallExpr 第一参数
// 的 BasicLit，不会扫到 godoc 或日志里的 "POSTGRES" 字样。
func TestEnvVerify_IgnoresCommentsAndOtherStrings(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	writeFile(t, filepath.Join(dir, "config", "config.go"), `package config

import "os"

// 这个注释里提到 GHOST_KEY 不应被识别为 env key。
const note = "GHOST_KEY also not a key"

func Load() {
	_ = os.Getenv("REAL_KEY")
}
`)
	writeFile(t, filepath.Join(dir, ".env.example"), `REAL_KEY=
`)

	code, out := runScript(t, dir, "env-verify.go")
	if code != 0 {
		t.Fatalf("env-verify exit=%d, expected 0\n%s", code, out)
	}
	if strings.Contains(out, "GHOST_KEY") {
		t.Errorf("GHOST_KEY should not be detected, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// architecture-verify

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

// ---------------------------------------------------------------------------
// new-endpoint

// newEndpointFixture 准备一个最小可注入的"假仓库"。脚本要求：
//   - internal/{handler,service,repository,model,task}/example.go 各一个
//   - internal/server.go 含 4 个 // NEH 锚点
//   - internal/router/router.go 含 2 个 // NEH 锚点
func newEndpointFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)

	for _, layer := range []string{"handler", "service", "repository", "model", "task"} {
		writeFile(t,
			filepath.Join(dir, "internal", layer, "example.go"),
			"package "+layer+"\n\n// Example placeholder for "+layer+".\ntype Example struct{}\n",
		)
	}

	writeFile(t, filepath.Join(dir, "internal", "server.go"), `package app

type handlers struct {
	Example string
	// NEH handlers-fields
}

func newHTTPHandlers() *handlers {
	example := "example"
	_ = example
	// NEH handlers-deps

	exampleH := "h"
	_ = exampleH
	// NEH handlers-construct

	return &handlers{
		Example: exampleH,
		// NEH handlers-return
	}
}
`)

	writeFile(t, filepath.Join(dir, "internal", "router", "router.go"), `package router

import "github.com/gin-gonic/gin"

type Dependencies struct {
	Example string
	// NEH deps-fields
}

func RegisterRoutes(r *gin.RouterGroup, deps Dependencies) error {
	registerExampleRoutes(r, deps)
	// NEH routes-register
	return nil
}

func registerExampleRoutes(r *gin.RouterGroup, deps Dependencies) {
	_ = r
	_ = deps
}
`)

	return dir
}

func TestNewEndpoint_InjectsAnchors(t *testing.T) {
	dir := newEndpointFixture(t)

	code, out := runScript(t, dir, "new-endpoint.go", "Order")
	if code != 0 {
		t.Fatalf("new-endpoint exit=%d, expected 0\n%s", code, out)
	}

	// 复制结果：5 个分层文件 + 3 个测试模板都该在。
	for _, p := range []string{
		"internal/handler/order.go",
		"internal/service/order.go",
		"internal/repository/order.go",
		"internal/model/order.go",
		"internal/task/order.go",
		"internal/handler/order_test.go",
		"internal/service/order_test.go",
		"internal/repository/order_test.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected file generated: %s\n%s", p, out)
		}
	}

	// server.go 应注入了 Order 相关行；锚点本身要保留（脚本插在锚点前）。
	serverBytes, err := os.ReadFile(filepath.Join(dir, "internal", "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	server := string(serverBytes)
	// 字段声明经 gofmt 对齐会有多空格——用 token 顺序匹配规避；其他无空白
	// 敏感的片段照常 strings.Contains。
	tokenChecks := [][]string{
		{"Order", "*handler.OrderHandler"},
		{"Order:", "orderH,"},
	}
	for _, toks := range tokenChecks {
		if !containsTokens(server, toks...) {
			t.Errorf("server.go missing tokens %v after injection", toks)
		}
	}
	for _, want := range []string{
		"orderRepository := repository.NewOrderRepository",
		"orderService := service.NewOrderService",
		"orderH := handler.NewOrderHandler",
		"// NEH handlers-fields",
		"// NEH handlers-deps",
		"// NEH handlers-construct",
		"// NEH handlers-return",
	} {
		if !strings.Contains(server, want) {
			t.Errorf("server.go missing %q after injection", want)
		}
	}

	// router.go 应同时拿到 Dependencies 字段 + 注册行 + register<Name>Routes 函数。
	routerBytes, err := os.ReadFile(filepath.Join(dir, "internal", "router", "router.go"))
	if err != nil {
		t.Fatal(err)
	}
	router := string(routerBytes)
	if !containsTokens(router, "Order", "*handler.OrderHandler") {
		t.Errorf("router.go missing Order field after injection")
	}
	for _, want := range []string{
		"registerOrderRoutes(r, deps)",
		"func registerOrderRoutes(",
		"// NEH deps-fields",
		"// NEH routes-register",
	} {
		if !strings.Contains(router, want) {
			t.Errorf("router.go missing %q after injection", want)
		}
	}
}

func TestNewEndpoint_RejectsDuplicate(t *testing.T) {
	dir := newEndpointFixture(t)

	// 第一次注入应成功。
	if code, out := runScript(t, dir, "new-endpoint.go", "Order"); code != 0 {
		t.Fatalf("first run exit=%d, expected 0\n%s", code, out)
	}

	// 第二次同名应被预检查拦截（拒绝覆盖已存在文件）。
	code, out := runScript(t, dir, "new-endpoint.go", "Order")
	if code == 0 {
		t.Fatalf("second run should fail (file exists)\n%s", out)
	}
	if !strings.Contains(out, "已存在") {
		t.Errorf("expected '已存在' in error, got:\n%s", out)
	}
}

func TestNewEndpoint_RejectsBadName(t *testing.T) {
	dir := newEndpointFixture(t)

	// 小写起头、非 CamelCase 形态，应被 camelCaseRe 拒。
	code, out := runScript(t, dir, "new-endpoint.go", "order")
	if code == 0 {
		t.Fatalf("expected reject for lower-case name\n%s", out)
	}
	if !strings.Contains(out, "CamelCase") {
		t.Errorf("expected CamelCase complaint, got:\n%s", out)
	}
}
