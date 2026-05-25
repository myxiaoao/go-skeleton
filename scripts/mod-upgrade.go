//go:build ignore

// mod-upgrade 把 go.mod 直接依赖升到最新 patch/minor。
//
// 入口：
//
//	go run scripts/mod-upgrade.go            # 干跑：列出会升的依赖
//	APPLY=1 go run scripts/mod-upgrade.go    # 真升：逐个 go get → tidy → verify
//	make mod-upgrade                          # 推荐：干跑
//	APPLY=1 make mod-upgrade                  # 真升
//
// 与旧 bash 版的语义差异：
//   - 直接解 `go list -m -u -json all` 的 JSON（用 encoding/json 流式），
//     不再依赖 jq——少一个工具依赖、跨平台。
//   - semver 解析走 `golang.org/x/mod/semver` 而不是手切 v1.2.3。能正确
//     比 v1.2.3-rc.1 这种带 pre-release 的版本，对 v0.x 严格走 unstable。
//   - apply 失败回滚：用 `git stash create` 锁定基线 commit hash 而不是
//     stash@{0} 索引，与 bash 版同 spirit、但走 git plumbing API 更准。
//
// 不属于任何包，//go:build ignore 让 go build/test 跳过它。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// goListModule 是 `go list -m -u -json all` 单条记录的字段子集。
// Indirect 字段对直接依赖会缺失（zero value false），按 false 判即可。
type goListModule struct {
	Path     string `json:"Path"`
	Version  string `json:"Version"`
	Indirect bool   `json:"Indirect"`
	Update   *struct {
		Path    string `json:"Path"`
		Version string `json:"Version"`
	} `json:"Update,omitempty"`
}

// upgrade 是一条待升项；split 给 apply 阶段用。
type upgrade struct {
	path string
	cur  string
	next string
}

func (u upgrade) String() string { return fmt.Sprintf("%s@%s", u.path, u.next) }

func main() {
	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		fatal(fmt.Errorf("chdir: %w", err))
	}

	updates, err := listUpdates()
	if err != nil {
		fatal(err)
	}

	if len(updates) == 0 {
		fmt.Println("no direct deps to upgrade.")
		return
	}

	var patchMinor, majorSkip []upgrade
	for _, u := range updates {
		if isMajorOrUnstableBump(u.cur, u.next) {
			majorSkip = append(majorSkip, u)
		} else {
			patchMinor = append(patchMinor, u)
		}
	}

	fmt.Println("==> direct deps with patch/minor updates (will upgrade)")
	if len(patchMinor) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, u := range patchMinor {
			fmt.Printf("  + %s@%s\n", u.path, u.next)
		}
	}

	fmt.Println()
	fmt.Println("==> major / v0.x updates (skipped — review manually)")
	if len(majorSkip) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, u := range majorSkip {
			fmt.Printf("  ! %s %s → %s\n", u.path, u.cur, u.next)
		}
	}

	if os.Getenv("APPLY") != "1" {
		fmt.Println()
		fmt.Println("dry run only. re-run with APPLY=1 to actually upgrade.")
		return
	}

	if len(patchMinor) == 0 {
		fmt.Println("nothing to apply.")
		return
	}

	if err := apply(patchMinor); err != nil {
		fatal(err)
	}
}

// listUpdates 跑 `go list -m -u -json all`，流式解 JSON（输出是若干 object
// 串联，不是 array），过滤出 .Update != nil && !.Indirect 的项。
func listUpdates() ([]upgrade, error) {
	cmd := exec.Command("go", "list", "-m", "-u", "-json", "all")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// stderr 透传：go list 会对网络问题等输出诊断，让用户看见。
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list -m -u -json all: %w", err)
	}

	dec := json.NewDecoder(&stdout)
	var out []upgrade
	for {
		var m goListModule
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		if m.Update == nil || m.Indirect {
			continue
		}
		out = append(out, upgrade{
			path: m.Path,
			cur:  m.Version,
			next: m.Update.Version,
		})
	}
	return out, nil
}

// isMajorOrUnstableBump 返回 true 当：
//   - cur 与 next 跨 semver major（v1 → v2），或
//   - cur 在 v0.x 段（任何改动按 unstable 处理，避免 v0.1.x → v0.2.x 静默
//     吃 API break）。
//
// 直接切首段不引第三方 semver 库：go.mod 里的版本号都是 `vX...` 形态
// （pseudo-version 也是），首个 `.` 前的段就是 vN。对 +incompatible /
// pre-release 后缀不敏感——它们都附在 minor/patch 段之后。
func isMajorOrUnstableBump(cur, next string) bool {
	curMajor := versionMajor(cur)
	nextMajor := versionMajor(next)
	if curMajor == "" || nextMajor == "" {
		// 没法解析当 unstable 处理，让用户人工 review。
		return true
	}
	if curMajor != nextMajor {
		return true
	}
	// v0.x.y 任何 minor / patch bump 都按 unstable 报，与 bash 版一致。
	return curMajor == "v0"
}

// versionMajor 提取 "v1.2.3" / "v1.2.3-rc.1" / "v0.x.y" → "v1" / "v1" / "v0"。
// 不是 v 开头返空串触发上层"按 unstable 处理"。
func versionMajor(v string) string {
	if !strings.HasPrefix(v, "v") {
		return ""
	}
	if i := strings.IndexByte(v, '.'); i > 0 {
		return v[:i]
	}
	// "v1" / "v2" 这种无次版本号的形态——很少见但合法。
	return v
}

// apply 逐个 go get + tidy + make verify。任一失败把 go.mod / go.sum
// 回滚到调用前的 baseline。
//
// baseline 用 `git stash create` 拿到一个 commit hash（不进 stash 栈，
// 不影响用户在另一个 shell stash），失败时 `git checkout <hash> -- go.mod go.sum`
// 还原。若调用前 working tree 干净，没有 stash hash，回滚走 `git checkout
// HEAD -- go.mod go.sum`。
func apply(items []upgrade) error {
	dirty, err := goModDirty()
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("go.mod / go.sum already dirty; commit or stash before running APPLY=1")
	}

	baselineStash, _ := gitOutput("stash", "create", "--", "go.mod", "go.sum")
	baselineStash = strings.TrimSpace(baselineStash)

	rollback := func() {
		args := []string{"checkout"}
		if baselineStash != "" {
			args = append(args, baselineStash)
		} else {
			args = append(args, "HEAD")
		}
		args = append(args, "--", "go.mod", "go.sum")
		_ = exec.Command("git", args...).Run()
	}

	fmt.Println()
	fmt.Println("==> applying upgrades (one at a time, with rollback on verify failure)")

	for _, u := range items {
		fmt.Printf("----- %s -----\n", u)

		if err := runStream("go", "get", u.String()); err != nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "✗ upgrade failed at: %s (go get)\n", u)
			fmt.Fprintln(os.Stderr, "  rolling back ALL upgrades to baseline (this run produced no changes)...")
			rollback()
			return err
		}
		if err := runStream("go", "mod", "tidy"); err != nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "✗ upgrade failed at: %s (go mod tidy)\n", u)
			fmt.Fprintln(os.Stderr, "  rolling back ALL upgrades to baseline...")
			rollback()
			return err
		}
		if err := runStream("make", "verify"); err != nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "✗ upgrade failed at: %s (make verify)\n", u)
			fmt.Fprintln(os.Stderr, "  rolling back ALL upgrades to baseline...")
			rollback()
			return err
		}
	}

	fmt.Println()
	fmt.Println("✓ all patch/minor upgrades applied + verify green.")
	fmt.Println("  review the diff (git diff go.mod go.sum) then commit.")
	return nil
}

func goModDirty() (bool, error) {
	err := exec.Command("git", "diff", "--quiet", "--", "go.mod", "go.sum").Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if !asExit(err, &exitErr) {
		return false, err
	}
	// `git diff --quiet` exit 1 = dirty，是预期信号而不是错误。
	if exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

func runStream(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

func repoRoot() (string, error) {
	out, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// asExit 把 err 转为 *exec.ExitError；不是的话返 false。exec.Cmd.Run 直接
// 返回 *exec.ExitError，不存在 wrap 链，所以不走 errors.As。
func asExit(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mod-upgrade:", err)
	os.Exit(1)
}
