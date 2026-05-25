# Error Codes

> 自动生成，不要手改。源：`pkg/errcode/common.go` + `pkg/response.MessageFor`。
> 重新生成：`make docs-errcodes`。CI 用 `make docs-errcodes-verify` 校验同步。

API 业务错误统一走 HTTP 200，错误信息靠下表的 `code` / `reason` 区分。

| Code | Reason | Default Message | Go Symbol |
|------|--------|-----------------|-----------|
| 1001 | `INVALID_PARAMS` | invalid request parameters | `errcode.InvalidParams` |
| 1002 | `UNAUTHORIZED` | unauthorized | `errcode.Unauthorized` |
| 1003 | `PERMISSION_DENIED` | permission denied | `errcode.PermissionDenied` |
| 1004 | `TOO_MANY_REQUESTS` | too many requests | `errcode.TooManyRequests` |
| 1005 | `REQUEST_TIMEOUT` | request timeout | `errcode.RequestTimeout` |
| 1006 | `SERVICE_DISABLED` | endpoint is disabled by configuration | `errcode.ServiceDisabled` |
| 9001 | `INTERNAL_ERROR` | internal server error | `errcode.InternalError` |
| 9002 | `DATABASE_ERROR` | database error | `errcode.DatabaseError` |
| 9003 | `QUEUE_UNAVAILABLE` | queue unavailable | `errcode.QueueUnavailable` |
| 9004 | `QUEUE_ERROR` | queue error | `errcode.QueueError` |
| 9005 | `NOT_IMPLEMENTED_YET` | not implemented yet | `errcode.NotImplementedYet` |
