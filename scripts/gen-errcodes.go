//go:build ignore

// gen-errcodes 把 pkg/errcode + pkg/response.MessageFor 的内容生成
// docs/errcodes.md。运行方式：
//
//	go run scripts/gen-errcodes.go
//
// 校验方式：make docs-errcodes-verify（CI 用）。
//
// 加新错误码时，除了在 pkg/errcode/common.go 加变量、在
// pkg/response.MessageFor 加 case 之外，**还要在下面 `entries` 列表里
// append 一行**。Verify 步骤会比对 git diff，遗漏会失败。
//
// 这是构建脚本，不属于任何包；用 //go:build ignore 排除掉，go build/test 不会编译它。

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"go-skeleton/pkg/errcode"
	"go-skeleton/pkg/response"
)

type entry struct {
	Name string
	Err  errcode.Error
}

func main() {
	entries := []entry{
		{"InvalidParams", errcode.InvalidParams},
		{"Unauthorized", errcode.Unauthorized},
		{"PermissionDenied", errcode.PermissionDenied},
		{"TooManyRequests", errcode.TooManyRequests},
		{"RequestTimeout", errcode.RequestTimeout},
		{"ServiceDisabled", errcode.ServiceDisabled},
		{"InternalError", errcode.InternalError},
		{"DatabaseError", errcode.DatabaseError},
		{"QueueUnavailable", errcode.QueueUnavailable},
		{"QueueError", errcode.QueueError},
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Err.Code() < entries[j].Err.Code() })

	var b strings.Builder
	b.WriteString("# Error Codes\n\n")
	b.WriteString("> 自动生成，不要手改。源：`pkg/errcode/common.go` + `pkg/response.MessageFor`。\n")
	b.WriteString("> 重新生成：`make docs-errcodes`。CI 用 `make docs-errcodes-verify` 校验同步。\n\n")
	b.WriteString("API 业务错误统一走 HTTP 200，错误信息靠下表的 `code` / `reason` 区分。\n\n")
	b.WriteString("| Code | Reason | Default Message | Go Symbol |\n")
	b.WriteString("|------|--------|-----------------|-----------|\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "| %d | `%s` | %s | `errcode.%s` |\n",
			e.Err.Code(), e.Err.Reason(), response.MessageFor(e.Err.Reason()), e.Name)
	}

	if err := os.WriteFile("docs/errcodes.md", []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write docs/errcodes.md:", err)
		os.Exit(1)
	}
	fmt.Println("docs/errcodes.md regenerated with", len(entries), "codes.")
}
