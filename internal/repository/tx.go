package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// txKey 是 context 里存放 GORM 事务实例的私有键。用自定义类型避免和别处
// 的 context value 冲突——string key 容易被外包覆盖。
type txKey struct{}

var (
	errNilDB   = errors.New("repository: database connection is required")
	errNilTxFn = errors.New("repository: transaction callback is required")
)

// WithTx 把活跃的 GORM 事务实例挂到 ctx 上，供 dbFromContext 在同一逻辑事务
// 里跨多个 repository 调用复用。一般由 InTx 内部使用，业务代码很少直接调。
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(normalizeContext(ctx), txKey{}, tx)
}

// InTx 在事务里执行 fn。如果 ctx 已经携带活跃事务（嵌套调用），直接复用
// 不再开新事务——避免内嵌 SAVEPOINT 让回滚语义变复杂。
//
// 用法：service 层用 InTx 包多个 repository 调用形成逻辑事务；repository
// 本身不调 InTx，只通过 dbFromContext 取当前事务句柄。
func InTx(ctx context.Context, db *gorm.DB, fn func(context.Context) error) error {
	if fn == nil {
		return errNilTxFn
	}
	if tx := txFromContext(ctx); tx != nil {
		return fn(normalizeContext(ctx))
	}
	if db == nil {
		return errNilDB
	}

	baseCtx := normalizeContext(ctx)
	return db.WithContext(baseCtx).Transaction(func(tx *gorm.DB) error {
		return fn(WithTx(baseCtx, tx))
	})
}

// dbFromContext 返回当前应该用的 *gorm.DB 实例：ctx 里有事务就用事务句柄，
// 没有就用 repository 持有的默认连接。所有 repository 方法都该走这一层，
// 否则在 InTx 包住的调用链里会用错连接、绕过事务。
func dbFromContext(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx := txFromContext(ctx); tx != nil {
		return tx
	}
	return db
}

// txFromContext 从 ctx 里取活跃事务句柄；没有返回 nil。
func txFromContext(ctx context.Context) *gorm.DB {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value(txKey{}).(*gorm.DB)
	return tx
}

// normalizeContext 把 nil ctx 兜底成 context.Background。GORM API 接到 nil
// ctx 会 panic，这层防御让上游万一传 nil 时不至于炸到 DB 层。
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
