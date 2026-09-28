package service

import (
	"context"
	"database/sql"
)

// Transactor 让 service 把多个 repository 调用串进一个事务。生产实现是
// repository.TxManager，在 internal/server.go 里装配。fn 内部一定要传
// txCtx 给 repository，否则会绕过事务。
type Transactor interface {
	InTx(ctx context.Context, fn func(txCtx context.Context) error) error
	InTxWithOptions(ctx context.Context, opts *sql.TxOptions, fn func(txCtx context.Context) error) error
}
