// env_verify_test.go 覆盖 env-verify.go 的回归：AST 扫 config/ 里
// os.Getenv / intEnv 等 key、跟 .env.example 对账，缺一个就 fail。
package scripts

import (
	"path/filepath"
	"strings"
	"testing"
)

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
