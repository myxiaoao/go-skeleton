// helpers_test.go 收纳 scripts/ 黑盒回归测试共享的 helper 与文档。
//
// 这些脚本本身是 main，没法被普通 import 调起来；用 t.TempDir 准备一个迷你
// "假仓库"，git init 后用 go run /abs/path/scripts/X.go 跑——脚本会把 cwd
// 当成仓库根来操作。比拆 testable 子包侵入小、比 mock fs 更贴真实行为。
//
// 测试按被测脚本拆分到独立文件:
//   - env_verify_test.go             env-verify
//   - architecture_verify_test.go    architecture-verify
//   - new_endpoint_test.go           new-endpoint (主流程 / 锚点注入 / x-resource)
//   - new_endpoint_check_test.go     new-endpoint-check (drift detector)
//   - new_endpoint_dto_test.go       new-endpoint --dto / DTO=1
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

// runScript 在 workdir 里跑脚本。两种模式：
//  1. 如果脚本只 import 标准库（env-verify / architecture-verify / docs-verify），
//     直接 `go run /abs/path/script.go`——workdir 不需要 go.mod。
//  2. 如果脚本 import 第三方库（new-endpoint 用到 kin-openapi），先在主仓库
//     用 `go build` 编出 binary（带主 go.mod 的依赖解析），再在 workdir 跑
//     这个 binary——workdir 不需要 go.mod。
//
// runScript 默认走模式 1。new-endpoint 测试自己用 runBinary 走模式 2。
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

// buildNewEndpoint 在主仓库 cwd 下用 go build 把 scripts/new-endpoint.go 编
// 成 tmp 可执行 binary，返回 binary 路径。共享给所有 new-endpoint 测试用例，
// 避免每个 case 重编一次。Cleanup 在 binary 与 t.TempDir 同生命周期。
func buildNewEndpoint(t *testing.T) string {
	t.Helper()
	scriptPath := filepath.Join(thisDir(t), "new-endpoint.go")
	// 在 scripts/ 同目录 build：这样 go 能找到主仓库的 go.mod。
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "new-endpoint")
	cmd := exec.Command("go", "build", "-o", binPath, scriptPath)
	cmd.Dir = thisDir(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build new-endpoint: %v\n%s", err, out)
	}
	return binPath
}

// runBinary 在 workdir 跑 binary 加 args，返回 exit code + combined output。
func runBinary(t *testing.T, workdir, binPath string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = workdir
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
	t.Fatalf("exec %s: %v\noutput:\n%s", binPath, err, out)
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
