# Error Codes

> 自动生成，不要手改。源：`pkg/errcode/common.go` + `pkg/response.MessageFor`。
> 重新生成：`make docs-errcodes`。CI 用 `make docs-errcodes-verify` 校验同步。

API 响应走统一信封 `{code, message, reason?, data?, metadata?}`；HTTP 状态码按下表映射，body `code` / `reason` 提供精确的业务分支信号。

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
