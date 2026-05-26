# Error Codes

> 自动生成，不要手改。源：`pkg/errcode/common.go` + `pkg/response.MessageFor`。
> 重新生成：`make docs-errcodes`。CI 用 `make docs-errcodes-verify` 校验同步。

API 响应走统一信封 `{code, message, reason?, data?, metadata?}`；HTTP 状态码按下表映射，body `code` / `reason` 提供精确的业务分支信号。

## 段位约定

一级段位决定 HTTP 状态码（见 `pkg/errcode.Error.HTTPStatus`）；二级 domain 段位给业务模块预留 namespace，避免后续冲突。

| 段位 | Domain | 说明 |
|------|--------|------|
| 1000-1099 | common | 通用客户端错误（INVALID_PARAMS / UNAUTHORIZED 等） |
| 1100-1199 | auth | 鉴权 / RBAC / 多租户特有客户端错误 |
| 1200-1299 | example | example 演示模块（fork 后通常删掉） |
| 1300-1999 | _reserved_ | 给后续业务模块（按字母序往后排：order / payment / ...） |
| 9000-9099 | common | 通用服务端错误（INTERNAL_ERROR / DATABASE_ERROR 等） |
| 9100-9199 | queue | 异步任务 / 消息队列服务端错误 |
| 9200-9999 | _reserved_ | 给基础设施增强（外部 API 失败 / 限流后端等） |

**加新模块时**：在 `pkg/errcode/common.go` 顶部注释追加该模块的 domain 段（如 `1300-1399 order`），同模块内的错误码连续编排。约定纯靠 review 维持——errcode 包不做运行期校验。

## 错误码清单

| Code | Reason | HTTP | Default Message | Go Symbol |
|------|--------|------|-----------------|-----------|
| 1001 | `INVALID_PARAMS` | 400 | invalid request parameters | `errcode.InvalidParams` |
| 1002 | `UNAUTHORIZED` | 401 | unauthorized | `errcode.Unauthorized` |
| 1003 | `PERMISSION_DENIED` | 403 | permission denied | `errcode.PermissionDenied` |
| 1004 | `TOO_MANY_REQUESTS` | 429 | too many requests | `errcode.TooManyRequests` |
| 1005 | `REQUEST_TIMEOUT` | 408 | request timeout | `errcode.RequestTimeout` |
| 1006 | `SERVICE_DISABLED` | 503 | endpoint is disabled by configuration | `errcode.ServiceDisabled` |
| 9001 | `INTERNAL_ERROR` | 500 | internal server error | `errcode.InternalError` |
| 9002 | `DATABASE_ERROR` | 500 | database error | `errcode.DatabaseError` |
| 9003 | `QUEUE_UNAVAILABLE` | 503 | queue unavailable | `errcode.QueueUnavailable` |
| 9004 | `QUEUE_ERROR` | 500 | queue error | `errcode.QueueError` |
| 9005 | `NOT_IMPLEMENTED_YET` | 501 | not implemented yet | `errcode.NotImplementedYet` |
