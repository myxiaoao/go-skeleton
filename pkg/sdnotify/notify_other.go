//go:build !linux

// Package sdnotify 在非 Linux 平台是 noop stub，让 cmd/api/main.go 跨平台
// 编译通过。本地 macOS / Windows 跑 go run 时不会调用 systemd。
package sdnotify

import (
	"context"
	"time"
)

// Ready 在非 Linux 平台是 noop。
func Ready() {}

// Watchdog 在非 Linux 平台只阻塞到 ctx.Done()，不发任何信号。
// 让 cmd/api/main.go 的 go sdnotify.Watchdog(ctx, ...) 在所有平台行为一致：
// 启动一个协程、ctx 取消时退出，仅 Linux 真正干活。
func Watchdog(ctx context.Context, _ time.Duration) {
	<-ctx.Done()
}
