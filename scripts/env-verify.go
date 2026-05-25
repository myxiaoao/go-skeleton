//go:build ignore

// env-verify 校验 config/ 里实际读取的 env key 与 .env.example 模板保持
// 同步。新增配置项忘了更新模板是高频疏忽——脚手架被复制后业务方读不到
// 模板里的字段会以为是"可选项"，结果上线踩到默认值不符合预期。
//
// 入口：
//
//	go run scripts/env-verify.go
//	make env-verify                          # 推荐
//
// 与旧 bash 版的语义差异（都是收紧）：
//   - 用 go/ast 解析 config/*.go：env key 必须是 helper(...) 的第一参数
//     且为基础字符串字面量；不会把字符串常量、注释、log 文本里的
//     "POSTGRES" 误命中（旧 grep 版偶尔会）。
//   - helper 列表是数据驱动的常量，加新 helper 改一行。
//
// 不属于任何包，//go:build ignore 让 go build/test 跳过它（与其他 scripts/*.go 同风格）。
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// envHelpers 是 config 包里读取 env key 的所有函数名。第一参数都是 env
// key 字面量（项目约定，看 config/config.go）。加新 helper 时往这里加一行。
var envHelpers = map[string]bool{
	"Getenv":          true, // os.Getenv
	"getEnvOrDefault": true,
	"boolEnv":         true,
	"intEnv":          true,
	"int64Env":        true,
	"durationEnv":     true,
	"environmentEnv":  true,
	"queueWeightsEnv": true,
}

// envKeyRe：env key 只允许大写字母 + 数字 + 下划线，首字符大写。
// 与 .env.example 行首 KEY=value 的 KEY 同形态。
var envKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// keyLoc 记录一个 key 在源里的出处，用于错误提示直接给可跳转的 file:line。
type keyLoc struct {
	file string
	line int
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		fatal(fmt.Errorf("chdir: %w", err))
	}

	if _, err := os.Stat(".env.example"); err != nil {
		fatal(fmt.Errorf(".env.example 不存在"))
	}

	configKeys, err := scanConfigKeys("config")
	if err != nil {
		fatal(err)
	}
	if len(configKeys) == 0 {
		fatal(fmt.Errorf("没从 config/ 提取到任何 env key——确认 envHelpers 列表是否还匹配现有 helper 签名"))
	}

	envKeys, err := scanEnvExample(".env.example")
	if err != nil {
		fatal(err)
	}
	if len(envKeys) == 0 {
		fatal(fmt.Errorf(".env.example 没有任何 KEY=... 行"))
	}

	// 双向差集。
	missingInExample := diff(configKeys, envKeys)
	missingInConfig := diff(envKeys, configKeys)

	// 当前没有需要白名单的"模板里有但代码不读"特例；保留 hook 备用。
	knownExampleOnly := map[string]bool{}
	if len(knownExampleOnly) > 0 {
		filtered := missingInConfig[:0]
		for _, k := range missingInConfig {
			if !knownExampleOnly[k] {
				filtered = append(filtered, k)
			}
		}
		missingInConfig = filtered
	}

	bad := false

	if len(missingInExample) > 0 {
		fmt.Fprintln(os.Stderr, "env-verify: config/ 读了下列 env key 但 .env.example 没列：")
		for _, k := range missingInExample {
			loc := configKeys[k]
			fmt.Fprintf(os.Stderr, "  - %s    (%s:%d)\n", k, loc.file, loc.line)
		}
		fmt.Fprintln(os.Stderr)
		bad = true
	}

	if len(missingInConfig) > 0 {
		fmt.Fprintln(os.Stderr, "env-verify: .env.example 列了下列 KEY 但 config/ 不读取（死字段或代码漏读）：")
		for _, k := range missingInConfig {
			loc := envKeys[k]
			fmt.Fprintf(os.Stderr, "  - %s    (%s:%d)\n", k, loc.file, loc.line)
		}
		fmt.Fprintln(os.Stderr)
		bad = true
	}

	if bad {
		fmt.Fprintln(os.Stderr, "env-verify: 修复方向——补 .env.example 里的字段、或者删 config 里没用的读取、或者把模板里的死字段清掉。")
		os.Exit(1)
	}

	fmt.Printf("env-verify: config/ ↔ .env.example 同步（%d keys）。\n", len(configKeys))
}

// scanConfigKeys 走 dir 下所有 .go（非 _test.go），解析后扫所有 CallExpr：
// Fun 是 envHelpers 里的 ident / selector，第一参数是基础字符串字面量
// 且形如 envKeyRe，记 key 到首个出现的位置。
func scanConfigKeys(dir string) (map[string]keyLoc, error) {
	keys := map[string]keyLoc{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callFuncName(call.Fun)
			if name == "" || !envHelpers[name] {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key := strings.Trim(lit.Value, `"`)
			if !envKeyRe.MatchString(key) {
				return true
			}
			if _, seen := keys[key]; !seen {
				pos := fset.Position(lit.Pos())
				keys[key] = keyLoc{file: path, line: pos.Line}
			}
			return true
		})
		return nil
	})
	return keys, err
}

// callFuncName 把 CallExpr.Fun 翻译成"函数名"——同包裸调用返 ident.Name，
// 跨包调用（如 os.Getenv）返 sel.Sel.Name。其他形态（方法调用 / 复杂表达式）
// 一律返空串，不参与匹配。
func callFuncName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	default:
		return ""
	}
}

// scanEnvExample 扫 .env.example 行首 `KEY=...` 提 KEY；忽略 # 注释和空行。
func scanEnvExample(path string) (map[string]keyLoc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// 形态：行首 KEY=value；KEY 必须以大写起头。
	// =value 部分不关心。允许 KEY= 后空（占位）。
	re := regexp.MustCompile(`^([A-Z][A-Z0-9_]*)=`)

	keys := map[string]keyLoc{}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		txt := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(txt), "#") {
			continue
		}
		m := re.FindStringSubmatch(txt)
		if m == nil {
			continue
		}
		key := m[1]
		if _, seen := keys[key]; !seen {
			keys[key] = keyLoc{file: path, line: line}
		}
	}
	return keys, sc.Err()
}

// diff 返回 a 里有 b 里没的 key 集合（排序后）。
func diff(a, b map[string]keyLoc) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "env-verify:", err)
	os.Exit(1)
}
