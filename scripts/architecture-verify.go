//go:build ignore

// architecture-verify 把 CLAUDE.md / AGENTS.md "分层规则" 段落里的 import
// 边界从"靠人/AI 记住"变成"机器拦截"。CI / make verify 会调它；失败时输出
// 违规文件 + 行号，直接定位。
//
// 入口：
//
//	go run scripts/architecture-verify.go
//	make architecture-verify                # 推荐
//
// 与旧 bash 版的语义差异（都是收紧）：
//   - 解析 *ast.File 的 Imports 而不是 grep "github.com/...": 不会误命中字符
//     串字面量、注释里的 import 路径、build-tag 之外的 import。
//   - context.Background 走 ast.Inspect 看 *ast.SelectorExpr "context.Background"
//     的 CallExpr：注释里 "context.Background()" 字样不会误报。
//   - 测试文件统一豁免（与旧版一致）。
//
// 规则一旦改动，同步更新 CLAUDE.md / AGENTS.md 的"分层规则"段。
//
// 不属于任何包，//go:build ignore 让 go build/test 跳过它（与 scripts/gen-errcodes.go 同风格）。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const modulePath = "go-skeleton"

// violation 是一条规则违例。file/line 直接给编辑器跳转。
type violation struct {
	rule int
	desc string
	file string
	line int
	note string // import path 或 "context.Background()" 这种附加上下文
}

func (v violation) String() string {
	if v.note != "" {
		return fmt.Sprintf("  %s:%d  %s", v.file, v.line, v.note)
	}
	return fmt.Sprintf("  %s:%d", v.file, v.line)
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "architecture-verify:", err)
		os.Exit(1)
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(os.Stderr, "architecture-verify: chdir:", err)
		os.Exit(1)
	}

	rules := []ruleSpec{
		// 规则 1：下游层禁止 import gin。service 也要被 worker 消费，绑 gin
		// 会让 worker 跑不通；下游层更不该接触 transport 框架。
		{
			id:   1,
			desc: "下游层禁止 import github.com/gin-gonic/gin",
			check: importInDirs(
				"github.com/gin-gonic/gin",
				"internal/service",
				"internal/repository",
				"internal/model",
				"internal/task",
				"internal/worker",
				"internal/taskqueue",
			),
		},
		// 规则 2：gorm.io/gorm 仅允许 repository / model / bootstrap / pkg/database。
		// 把"允许列表外的全部目录"翻译成具体路径前缀，比"扫全仓再排除"更清晰。
		{
			id:   2,
			desc: "gorm.io/gorm 仅允许 internal/{repository,model,bootstrap} 与 pkg/database 使用",
			check: importExcept(
				"gorm.io/gorm",
				"internal/repository",
				"internal/model",
				"internal/bootstrap",
				"pkg/database",
			),
		},
		// 规则 3：pkg/ 禁止反向依赖 internal/*。pkg/ 是通用工具，理论上可
		// 被其他项目复用；反向依赖会让 pkg 失去这个属性。
		{
			id:   3,
			desc: "pkg/ 禁止 import go-skeleton/internal/*",
			check: importPrefixInDirs(
				modulePath+"/internal/",
				"pkg",
			),
		},
		// 规则 4：service / handler 运行时代码禁止 context.Background()。这
		// 两层一定有外部传入的 ctx（HTTP request / asynq task），用 Background
		// 会丢 trace_id / 超时。bootstrap / server.go 起后台 goroutine 是合法
		// 例外，不在本规则范围。
		{
			id:   4,
			desc: "service / handler 运行时代码禁止 context.Background()（应当透传外部 ctx）",
			check: contextBackgroundInDirs(
				"internal/service",
				"internal/handler",
			),
		},
	}

	var all []violation
	for _, r := range rules {
		vs, err := r.check()
		if err != nil {
			fmt.Fprintf(os.Stderr, "architecture-verify: rule %d: %v\n", r.id, err)
			os.Exit(2)
		}
		for i := range vs {
			vs[i].rule = r.id
			vs[i].desc = r.desc
		}
		all = append(all, vs...)
	}

	if len(all) == 0 {
		fmt.Println("architecture-verify: 4 import / context rules clean.")
		return
	}

	// 按 rule → file → line 排序，输出稳定。
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].rule != all[j].rule {
			return all[i].rule < all[j].rule
		}
		if all[i].file != all[j].file {
			return all[i].file < all[j].file
		}
		return all[i].line < all[j].line
	})

	// 按 rule 分组打印。
	var curRule int
	for _, v := range all {
		if v.rule != curRule {
			if curRule != 0 {
				fmt.Fprintln(os.Stderr)
			}
			fmt.Fprintf(os.Stderr, "architecture-verify: [rule %d] %s\n", v.rule, v.desc)
			curRule = v.rule
		}
		fmt.Fprintln(os.Stderr, v.String())
	}

	// 按 rule 去重统计——同一规则多条违例算一条规则违反，与旧 bash 版统计口径一致。
	rulesHit := map[int]struct{}{}
	for _, v := range all {
		rulesHit[v.rule] = struct{}{}
	}
	fmt.Fprintf(os.Stderr, "\narchitecture-verify: %d rule(s) violated. 详见上面的文件:行号清单。\n", len(rulesHit))
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// 规则定义

type ruleSpec struct {
	id    int
	desc  string
	check func() ([]violation, error)
}

// importInDirs 在 dirs 下检查"任何文件 import path == imp"。
func importInDirs(imp string, dirs ...string) func() ([]violation, error) {
	return func() ([]violation, error) {
		var vs []violation
		for _, d := range dirs {
			more, err := walkImports(d, func(file string, spec *ast.ImportSpec) *violation {
				if importPath(spec) != imp {
					return nil
				}
				return &violation{
					file: file,
					line: lineOf(spec.Pos()),
					note: fmt.Sprintf(`import %q`, imp),
				}
			})
			if err != nil {
				return nil, err
			}
			vs = append(vs, more...)
		}
		return vs, nil
	}
}

// importExcept 在仓库根下扫所有 .go，import path == imp 且文件不在 allow
// 任一前缀下视为违规。
func importExcept(imp string, allow ...string) func() ([]violation, error) {
	return func() ([]violation, error) {
		return walkImports(".", func(file string, spec *ast.ImportSpec) *violation {
			if importPath(spec) != imp {
				return nil
			}
			for _, a := range allow {
				if strings.HasPrefix(file, a+string(os.PathSeparator)) {
					return nil
				}
			}
			return &violation{
				file: file,
				line: lineOf(spec.Pos()),
				note: fmt.Sprintf(`import %q`, imp),
			}
		})
	}
}

// importPrefixInDirs 在 dirs 下检查"任何文件 import path 以 prefix 开头"。
func importPrefixInDirs(prefix string, dirs ...string) func() ([]violation, error) {
	return func() ([]violation, error) {
		var vs []violation
		for _, d := range dirs {
			more, err := walkImports(d, func(file string, spec *ast.ImportSpec) *violation {
				p := importPath(spec)
				if !strings.HasPrefix(p, prefix) {
					return nil
				}
				return &violation{
					file: file,
					line: lineOf(spec.Pos()),
					note: fmt.Sprintf(`import %q`, p),
				}
			})
			if err != nil {
				return nil, err
			}
			vs = append(vs, more...)
		}
		return vs, nil
	}
}

// contextBackgroundInDirs 在 dirs 下扫所有 .go（非 _test.go），找
// `context.Background()` 调用——不是字符串字面量也不是 import 别名。
func contextBackgroundInDirs(dirs ...string) func() ([]violation, error) {
	return func() ([]violation, error) {
		var vs []violation
		for _, d := range dirs {
			more, err := walkCalls(d, func(file string, call *ast.CallExpr) *violation {
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return nil
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return nil
				}
				if ident.Name != "context" || sel.Sel.Name != "Background" {
					return nil
				}
				return &violation{
					file: file,
					line: lineOf(call.Pos()),
					note: "context.Background()",
				}
			})
			if err != nil {
				return nil, err
			}
			vs = append(vs, more...)
		}
		return vs, nil
	}
}

// ---------------------------------------------------------------------------
// AST walker

// fset 全局共享：所有 parse 都走这个 token.FileSet，position 换 line 才一致。
var fset = token.NewFileSet()

func walkImports(dir string, hit func(file string, spec *ast.ImportSpec) *violation) ([]violation, error) {
	var vs []violation
	err := walkGoFiles(dir, func(path string, f *ast.File) {
		for _, imp := range f.Imports {
			if v := hit(path, imp); v != nil {
				vs = append(vs, *v)
			}
		}
	})
	return vs, err
}

func walkCalls(dir string, hit func(file string, call *ast.CallExpr) *violation) ([]violation, error) {
	var vs []violation
	err := walkGoFiles(dir, func(path string, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if v := hit(path, call); v != nil {
				vs = append(vs, *v)
			}
			return true
		})
	})
	return vs, err
}

// walkGoFiles 走 dir 下所有 .go（非 _test.go），解析后回调 visit。
func walkGoFiles(dir string, visit func(path string, f *ast.File)) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil // 目录不存在视作"无违例"，与旧 bash 版 [ -d ] 守卫一致。
	}
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// 跳掉 vendor / .git / dist 等明显非源码目录。
			name := d.Name()
			if name == "vendor" || name == ".git" || name == "dist" || name == "bin" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// parser.ImportsOnly 不行——规则 4 要看 *ast.CallExpr。统一全文 parse，
		// 比启发式优化更省心；当前仓库规模 parse 毫秒级。
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		visit(path, f)
		return nil
	})
}

// importPath 把 *ast.ImportSpec.Path.Value（带引号）剥成裸路径。
func importPath(spec *ast.ImportSpec) string {
	v := spec.Path.Value
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

func lineOf(p token.Pos) int {
	return fset.Position(p).Line
}

// repoRoot 用 git 拿仓库根，与 drop-example.go 一致。
func repoRoot() (string, error) {
	out, err := runGitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func runGitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}
