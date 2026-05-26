package errcode

// 错误码分段约定：
//
//	1000-1999  客户端错误（请求 / 鉴权 / 限流 / 业务规则前置）
//	9000-9999  服务端错误（基础设施 / 兜底 / 异步任务失败）
//
// 一级段位（客户端 vs 服务端）由 errcode → HTTP 状态映射决定（见
// pkg/errcode.Error.HTTPStatus）；不要把客户端错放进 9xxx，否则 HTTP
// status 自动落到 5xx，监控误判服务异常。
//
// **二级 domain 段位（给业务模块预留 namespace，避免后续冲突）**：
//
//	1000-1099  common      通用客户端错误（INVALID_PARAMS / UNAUTHORIZED 等）
//	1100-1199  auth        鉴权 / RBAC / 多租户特有客户端错误
//	1200-1299  example     example 演示模块（fork 后通常删掉）
//	1300-1399  <reserved>  预留给下一个业务模块（按字母序往后排）
//	...
//	9000-9099  common      通用服务端错误（INTERNAL_ERROR / DATABASE_ERROR 等）
//	9100-9199  queue       异步任务 / 消息队列服务端错误
//	9200-9299  <reserved>  预留给基础设施增强（如外部 API 调用失败、限流后端错误）
//
// **加新模块时**：在本注释里追加该模块的 domain 段（如 `1300-1399 order`），
// 同模块内的错误码连续编排（1300/1301/1302...），不要散在多个段位里。这条
// 约定纯靠 review 维持——errcode 包不做运行期校验，因为段位划分本身是
// 治理决策不是技术约束。
//
// 新增错误码同步改 pkg/response.MessageFor + 跑 make docs-errcodes。
var (
	// InvalidParams 表示请求体或 query 参数校验失败。
	InvalidParams = newError(1001, "INVALID_PARAMS")
	// Unauthorized 表示需要鉴权或鉴权无效（token 缺失 / 过期 / 验签失败）。
	Unauthorized = newError(1002, "UNAUTHORIZED")
	// PermissionDenied 表示已鉴权但无操作权限（RBAC 拦下）。
	PermissionDenied = newError(1003, "PERMISSION_DENIED")
	// TooManyRequests 表示请求被限流。
	TooManyRequests = newError(1004, "TOO_MANY_REQUESTS")
	// RequestTimeout 表示请求处理超过配置的 RequestTimeout 截止时间。
	RequestTimeout = newError(1005, "REQUEST_TIMEOUT")
	// ServiceDisabled 表示端点存在于 OpenAPI 契约里、但被配置开关关掉了
	// （例如开发期路由在生产环境保持禁用），保持 spec 与运行时行为对齐。
	ServiceDisabled = newError(1006, "SERVICE_DISABLED")

	// InternalError 是所有未识别 server 侧错误的兜底。
	InternalError = newError(9001, "INTERNAL_ERROR")
	// DatabaseError 包裹 service 层暴露的持久化失败（GORM 错误透传给客户端
	// 会泄漏 schema，所以统一压成这一个码）。
	DatabaseError = newError(9002, "DATABASE_ERROR")
	// QueueUnavailable 表示请求要投异步任务，但队列未配置或不可用。
	QueueUnavailable = newError(9003, "QUEUE_UNAVAILABLE")
	// QueueError 包裹 service 层暴露的异步任务投递失败。
	QueueError = newError(9004, "QUEUE_ERROR")
	// NotImplementedYet 给 make new-endpoint 生成的方法骨架占位。
	// 业务实现填上后应换成具体错误码（或返 nil 成功）；保留这个值是为了
	// 让骨架 make verify 通过 + 跑起来时给出明确的"未实现"信号，而不是
	// 静默返 200 误导调用方。
	NotImplementedYet = newError(9005, "NOT_IMPLEMENTED_YET")
)
