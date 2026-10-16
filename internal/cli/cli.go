// Package cli 是单二进制 go-skeleton 的子命令实现：
//
//	go-skeleton                          # 等同 all：单进程内 migrate up + API + Worker
//	go-skeleton all                      # 同上
//	go-skeleton api                      # HTTP 服务
//	go-skeleton worker                   # Asynq 消费者
//	go-skeleton migrate [up|down|status] # goose 迁移，默认 up
//	go-skeleton version                  # 打印版本信息
//
// api / worker / migrate 可作为相互独立的进程部署（各自扩缩容、各自 systemd unit /
// k8s 工作负载）；all 适合小规模单机部署，一个进程跑全部。cmd/go-skeleton/main.go 只负责把 os.Args 交给 Run。
package cli

import (
	"fmt"
	"os"
	"slices"

	"go-skeleton/pkg/buildinfo"
)

const usage = `Usage: go-skeleton [command] [args]

Commands:
  all                      (default) run migrate up, then API + worker in one process
  api                      start HTTP API server
  worker                   start Asynq worker
  migrate [up|down|status] run database migrations (default: up)
  version                  print version info
`

// command 是一个子命令入口，args 为子命令名之后的参数。
type command func(args []string) error

var commands = map[string]command{
	"all":     runAll,
	"api":     runAPI,
	"worker":  runWorker,
	"migrate": runMigrate,
	"version": runVersion,
}

// Run 按 args[0] 分发子命令，返回进程退出码。
func Run(args []string) int {
	if len(args) == 0 {
		args = []string{"all"}
	}
	name := args[0]
	// 兼容旧入口的 -version / --version 习惯。
	if slices.Contains([]string{"-version", "--version"}, name) {
		name = "version"
	}
	if slices.Contains([]string{"-h", "--help", "help"}, name) {
		fmt.Fprint(os.Stdout, usage)
		return 0
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, usage)
		return 2
	}
	if err := cmd(args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runVersion([]string) error {
	fmt.Println(buildinfo.String())
	return nil
}

// noArgs 校验不接受参数的子命令（api / worker），避免拼错参数被静默忽略。
func noArgs(name string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s: unexpected arguments %v", name, args)
	}
	return nil
}
