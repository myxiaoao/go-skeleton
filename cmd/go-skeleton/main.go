// go-skeleton 单二进制入口：只把命令行参数交给 internal/cli 分发子命令。
package main

import (
	"os"

	"go-skeleton/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
