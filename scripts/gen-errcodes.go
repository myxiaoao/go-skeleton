//go:build ignore

// gen-errcodes 把 pkg/errcode + pkg/response.MessageFor 的内容生成
// docs/errcodes.md。运行方式：
//
//	go run scripts/gen-errcodes.go
//
// 校验方式：make docs-errcodes-verify（CI 用）。
//
// 实现说明：
//   errcode 变量名（"InvalidParams" 这个字符串）没法通过反射拿——
//   reflect 看到的是 errcode.Error{code, reason} struct 本身，丢失了
//   左值名字。所以这里走 go/parser 直接解析 pkg/errcode/common.go 的
//   AST，扫所有形如 `XxxName = newError(NNN, "REASON")` 的 ValueSpec。
//
//   这样加新错误码只需要改 pkg/errcode/common.go + pkg/response.MessageFor，
//   不用再来这里维护清单——脚本自动发现。
//
// 这是构建脚本，不属于任何包；用 //go:build ignore 排除掉，go build/test 不会编译它。

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"

	"go-skeleton/pkg/response"
)

type entry struct {
	Name   string
	Code   int
	Reason string
}

func main() {
	entries, err := discoverEntries("pkg/errcode/common.go")
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover errcodes:", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no errcodes discovered in pkg/errcode/common.go")
		os.Exit(1)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Code < entries[j].Code })

	var b strings.Builder
	b.WriteString("# Error Codes\n\n")
	b.WriteString("> 自动生成，不要手改。源：`pkg/errcode/common.go` + `pkg/response.MessageFor`。\n")
	b.WriteString("> 重新生成：`make docs-errcodes`。CI 用 `make docs-errcodes-verify` 校验同步。\n\n")
	b.WriteString("API 业务错误统一走 HTTP 200，错误信息靠下表的 `code` / `reason` 区分。\n\n")
	b.WriteString("| Code | Reason | Default Message | Go Symbol |\n")
	b.WriteString("|------|--------|-----------------|-----------|\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "| %d | `%s` | %s | `errcode.%s` |\n",
			e.Code, e.Reason, response.MessageFor(e.Reason), e.Name)
	}

	if err := os.WriteFile("docs/errcodes.md", []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write docs/errcodes.md:", err)
		os.Exit(1)
	}
	fmt.Println("docs/errcodes.md regenerated with", len(entries), "codes.")
}

// discoverEntries 解析 path 的 AST，找出形如
//
//	XxxName = newError(NNN, "REASON")
//
// 的全部声明。只认 newError 这个函数名（pkg/errcode 包内唯一构造入口）；
// 任何其他形式（直接 Error{...}、跨包工厂函数等）都不会被收录。
func discoverEntries(path string) ([]entry, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var out []entry
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// 一个 ValueSpec 可能是 `a, b = ..., ...`；按下标对齐 Names 与 Values。
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					break
				}
				call, ok := vs.Values[i].(*ast.CallExpr)
				if !ok {
					continue
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok || ident.Name != "newError" {
					continue
				}
				if len(call.Args) != 2 {
					return nil, fmt.Errorf("%s: newError(%s) expects 2 args, got %d", path, name.Name, len(call.Args))
				}
				codeLit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || codeLit.Kind != token.INT {
					return nil, fmt.Errorf("%s: newError(%s) first arg must be int literal", path, name.Name)
				}
				reasonLit, ok := call.Args[1].(*ast.BasicLit)
				if !ok || reasonLit.Kind != token.STRING {
					return nil, fmt.Errorf("%s: newError(%s) second arg must be string literal", path, name.Name)
				}
				code, err := strconv.Atoi(codeLit.Value)
				if err != nil {
					return nil, fmt.Errorf("%s: newError(%s) code parse: %w", path, name.Name, err)
				}
				reason, err := strconv.Unquote(reasonLit.Value)
				if err != nil {
					return nil, fmt.Errorf("%s: newError(%s) reason parse: %w", path, name.Name, err)
				}
				out = append(out, entry{Name: name.Name, Code: code, Reason: reason})
			}
		}
	}
	return out, nil
}
