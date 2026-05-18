package config

import "github.com/joho/godotenv"

// LoadEnv 预加载可选的 dotenv 文件，**不**覆盖已存在的环境变量。
// 加载顺序：caller 传入的 paths 在前，根目录 .env 在后；godotenv.Load 不覆
// 写已存在的 env，所以最终生效优先级是：真实环境变量 > paths[0] > .env。
// 让 cmd/<proc>/.env 能做差异化覆盖、根目录 .env 兜底，运维传 env 优先级最高。
func LoadEnv(paths ...string) {
	files := append([]string{}, paths...)
	files = append(files, ".env")

	for _, file := range files {
		_ = godotenv.Load(file)
	}
}
