// Package buildinfo 持有当前二进制的构建期身份信息（版本 / commit / 构建时间）。
//
// 值在链接期通过 -ldflags 注入（见 Makefile）：
//
//	-X 'go-skeleton/pkg/buildinfo.Version=...'
//	-X 'go-skeleton/pkg/buildinfo.Commit=...'
//	-X 'go-skeleton/pkg/buildinfo.BuildTime=...'
//
// 没经 Makefile 的 `go run` / `go build` 命令也会有合理默认值，避免下游
// 拿到空字符串需要再做兜底。
package buildinfo

import "fmt"

// 构建期元数据。由项目 Makefile 在链接期 -X 注入；这里给的默认值故意非空，
// 让消费方不用做 nil / 空串守卫。
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

// String 返回一行人读的版本摘要，用于启动日志和 `binary -version` 输出。
func String() string {
	return fmt.Sprintf("version=%s commit=%s buildTime=%s", Version, Commit, BuildTime)
}

// Map 把元数据返成结构化 map，供 /health 这种 JSON 响应和 zap 日志字段使用。
func Map() map[string]string {
	return map[string]string{
		"version":    Version,
		"commit":     Commit,
		"build_time": BuildTime,
	}
}
