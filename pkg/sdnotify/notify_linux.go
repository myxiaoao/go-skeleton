//go:build linux

// Package sdnotify 给 systemd Type=notify 服务发 READY/WATCHDOG 信号。
//
// 只在 Linux 编译；其他平台用 notify_other.go 里的 noop stub。这样开发机
// （macOS）和 CI 都能 build，不强依赖 systemd 环境。
package sdnotify

import (
	"context"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
)

// Ready 发一次 READY=1，告诉 systemd "进程已经能服务请求"。Type=notify 时
// 不发这条 systemd 会一直等到 TimeoutStartSec。非 systemd 启动（如本地
// go run）daemon.SdNotify 直接返 false,nil，安全无副作用。
func Ready() {
	_, _ = daemon.SdNotify(false, daemon.SdNotifyReady)
}

// Watchdog 周期发 WATCHDOG=1 心跳。systemd 在 WatchdogSec 内没收到就视为
// 进程挂死，按 unit 的 Restart 策略重启。interval 建议为 WatchdogSec 的 1/3
// 避免抖动误杀。ctx 取消时优雅退出循环。
//
// 不在这里发 READY=1——READY 表示"已能服务请求"，必须由调用方在真正就绪
// （HTTP 端口绑定成功 / worker 开始消费）时显式调 Ready()。Watchdog 一启动
// 就发 READY 是过度乐观：进程刚拉起、端口还没绑就告诉 systemd 已就绪，
// 绑定若失败会导致 systemd 误判启动成功。
func Watchdog(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = daemon.SdNotify(false, daemon.SdNotifyWatchdog)
		}
	}
}
