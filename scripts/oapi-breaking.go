//go:build ignore

// oapi-breaking 用 oasdiff 检测 OpenAPI 破坏性变更。
//
// 入口：
//
//	go run scripts/oapi-breaking.go
//	make oapi-breaking                       # 推荐
//	OAPI_BREAKING_BASE_REF=v0.1.0 make oapi-breaking
//	OAPI_ALLOW_BREAKING=1 make oapi-breaking # 故意 expand-contract 时跳过
//
// 行为（与旧 bash 版语义一致）：
//   - 校验 oasdiff CLI 可用，不在则 `go install` pin 版本到 GOBIN。
//   - 校验 base git ref 存在；同 SHA 自比时直接跳过。
//   - 调 `oasdiff breaking --fail-on ERR <base>:api/openapi.yaml api/openapi.yaml`，
//     非零退出码透传给 make。
//
// 不接进 make verify 的理由（参见 docs/runbook.md "OpenAPI 破坏性变更检查"
// 段）：本地不一定有 fresh origin/master，且 expand-contract 阶段会故意 breaking。
// 定位是 PR / 发版前门禁。
//
// 配置（与旧 bash 版同名 env）：
//   - OAPI_BREAKING_BASE_REF  默认 origin/master
//   - OAPI_BREAKING_SPEC      默认 api/openapi.yaml
//   - OAPI_ALLOW_BREAKING     非空时直接跳过返 0
//   - OASDIFF_VERSION         pin 版本，默认 v1.16.0
//
// 不属于任何包，//go:build ignore 让 go build/test 跳过它。
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	defaultBaseRef        = "origin/master"
	defaultSpec           = "api/openapi.yaml"
	defaultOasdiffVersion = "v1.16.0"
)

func main() {
	if v := os.Getenv("OAPI_ALLOW_BREAKING"); v != "" {
		fmt.Println(`oapi-breaking: OAPI_ALLOW_BREAKING set, skipping (PR 描述里写明 expand-contract 缘由).`)
		return
	}

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		fatal(fmt.Errorf("chdir: %w", err))
	}

	spec := envOr("OAPI_BREAKING_SPEC", defaultSpec)
	baseRef := envOr("OAPI_BREAKING_BASE_REF", defaultBaseRef)
	oasdiffVersion := envOr("OASDIFF_VERSION", defaultOasdiffVersion)

	if _, err := os.Stat(spec); err != nil {
		fatal(fmt.Errorf("%s not found", spec))
	}

	if err := ensureOasdiff(oasdiffVersion); err != nil {
		fatal(err)
	}

	if err := requireGitRef(baseRef); err != nil {
		fatal(err)
	}

	// 同 SHA 自比直接跳过：HEAD == BASE 时调 oasdiff 会得到一个总是 "no
	// changes" 的输出，没有意义但徒增 CI 耗时。
	headSHA, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		fatal(fmt.Errorf("git rev-parse HEAD: %w", err))
	}
	baseSHA, err := gitOutput("rev-parse", baseRef)
	if err != nil {
		fatal(fmt.Errorf("git rev-parse %s: %w", baseRef, err))
	}
	if strings.TrimSpace(headSHA) == strings.TrimSpace(baseSHA) {
		fmt.Printf("oapi-breaking: HEAD == %s (%s), nothing to compare.\n",
			baseRef, strings.TrimSpace(baseSHA))
		return
	}

	fmt.Printf("oapi-breaking: comparing %s:%s vs working tree %s ...\n\n", baseRef, spec, spec)

	// --fail-on ERR：只有 ERR 级（确凿 breaking）才退出非零，WARN/INFO 放过去。
	// --format text：人类可读；CI 也吃 stdout 当 PR comment 原料。
	// base 写 "<ref>:<path>" git ref 语法；revision 写工作树文件路径。
	cmd := exec.Command("oasdiff", "breaking",
		"--fail-on", "ERR",
		"--format", "text",
		baseRef+":"+spec,
		spec,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			fmt.Fprintf(os.Stderr,
				"oapi-breaking: ERR-level breaking changes detected (exit=%d).\n",
				exitErr.ExitCode())
		} else {
			fmt.Fprintf(os.Stderr, "oapi-breaking: oasdiff failed: %v\n", err)
		}
		fmt.Fprintln(os.Stderr,
			"              If this is an expand-contract change you've coordinated,")
		fmt.Fprintln(os.Stderr,
			"              re-run with OAPI_ALLOW_BREAKING=1 and document it in the PR.")
		// 优先透传 oasdiff 的 exit code，找不到就用 1。
		if exitErr != nil {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("oapi-breaking: no ERR-level breaking changes.")
}

// ensureOasdiff 检查 PATH 上有 oasdiff，没有则 `go install` pin 版本。
//
// 不强匹配版本：装新一点没事，但首次运行时 go install 会从 GOPATH/bin 找。
// 若仍找不到则提示 PATH 配置问题。
func ensureOasdiff(version string) error {
	if path, err := exec.LookPath("oasdiff"); err == nil {
		// 不强校版本：装高了一般兼容。打印实际版本便于排查。
		out, _ := exec.Command(path, "--version").Output()
		fmt.Printf("oasdiff %s: ok\n", strings.TrimSpace(string(out)))
		return nil
	}
	fmt.Printf("Installing oasdiff %s ...\n", version)
	cmd := exec.Command("go", "install", "github.com/oasdiff/oasdiff@"+version)
	// 临时清掉 GOFLAGS 避免 mod=vendor 等设置干扰 install。
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install oasdiff: %w", err)
	}
	if _, err := exec.LookPath("oasdiff"); err != nil {
		return fmt.Errorf(`oasdiff install succeeded but binary not on PATH.
Check that $(go env GOBIN) or $(go env GOPATH)/bin is in PATH.`)
	}
	return nil
}

// requireGitRef 校验 git ref 在本仓库可解析。失败给 fetch-depth 提示。
func requireGitRef(ref string) error {
	if err := exec.Command("git", "rev-parse", "--verify", "--quiet", ref).Run(); err != nil {
		return fmt.Errorf(`git ref %q not found.

If you're in CI, ensure actions/checkout has fetch-depth: 0 so the base ref
is in the local clone. If you're local, fetch first:

  git fetch origin master

Then re-run. Or override the base ref:

  OAPI_BREAKING_BASE_REF=<ref> make oapi-breaking`, ref)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func repoRoot() (string, error) {
	out, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "oapi-breaking:", err)
	os.Exit(1)
}
