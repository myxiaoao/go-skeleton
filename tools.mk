# 工具链版本 pin（单独成文件）。CI 的工具缓存 key 只 hashFiles 本文件，
# 改 Makefile 其他内容不会让缓存失效；升级工具时改这里 + 跑 make init。

# 工具链版本固定。升级时改这里 + 跑 make init 重新装，让 CI / 队友复现一致。
GOLANGCI_LINT_VERSION ?= v2.13.2
OAPI_CODEGEN_VERSION  ?= v2.7.0
SQLC_VERSION          ?= v1.31.1
# 格式化工具：gofumpt 收紧 gofmt 风格细节（多余空行、struct 对齐等），
# gci 用显式 sections 控制 import 分组（standard / default / prefix），
# 避免短 module name（go-skeleton 不含 dot）被误判成 stdlib 的老坑。
GCI_VERSION           ?= v0.14.0
GOFUMPT_VERSION       ?= v0.10.0
# 安全扫描工具。govulncheck 查已公布的 CVE；gosec 静态扫描代码里的安全反模式
# （硬编码密钥、SQL 拼接、不安全的随机数等）。两者跑独立 target，不进 verify
# 默认链路——CVE 数据库更新会让 verify 变成 flaky；CI 单独跑 make sec。
GOVULNCHECK_VERSION   ?= v1.8.0
GOSEC_VERSION         ?= v2.29.0
# air：本地热重载。仅在 make watch 时按需安装，不进 init 默认链路，
# 避免新人 clone 后被强制装一个开发期可选工具。
AIR_VERSION           ?= v1.62.0
