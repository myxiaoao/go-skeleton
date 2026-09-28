package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go-skeleton/internal/repository/sqlcdb"
)

// DB 是 repository 需要的数据库能力：跑查询（sqlc 的 DBTX）+ 开事务。
// *pgxpool.Pool 直接满足这个接口。
type DB interface {
	sqlcdb.DBTX
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// txKey 是 context 里存放活跃事务句柄的私有键。
type txKey struct{}

var (
	errNilDB   = errors.New("repository: database connection is required")
	errNilTxFn = errors.New("repository: transaction callback is required")
)

// WithTx 把活跃事务挂到 ctx 上，供 dbFromContext 在同一逻辑事务里跨多个
// repository 调用复用。一般只由 InTx 内部调用。
func WithTx(ctx context.Context, tx sqlcdb.DBTX) context.Context {
	return context.WithValue(normalizeContext(ctx), txKey{}, tx)
}

// InTx 在事务里执行 fn，用默认 isolation（Postgres 下是 READ COMMITTED）。
// 如果 ctx 已经携带活跃事务（嵌套调用），直接复用不再开新事务——避免
// 嵌套开 SAVEPOINT 让语义变复杂。
//
// service 层用 InTx 包多个 repository 调用形成跨 repository 事务；
// repository 本身不调 InTx，只通过 dbFromContext 取当前事务句柄。
func InTx(ctx context.Context, db DB, fn func(context.Context) error) error {
	return InTxWithOptions(ctx, db, nil, fn)
}

// InTxWithOptions 在事务里执行 fn，opts 支持自定义 isolation
// （如 sql.LevelRepeatableRead / sql.LevelSerializable）和 ReadOnly。
// opts 为 nil 时等价于 InTx。
//
// 嵌套行为：如果 ctx 已经携带活跃事务，**忽略 opts** 直接复用——isolation
// 只能在 BEGIN 时决定，子调用改不了，调用方要决定隔离级别必须在最外层
// InTx/InTxWithOptions 调用点决定。
//
// commit / rollback 走 pgx.BeginTxFunc：fn 返回 nil 提交，返回 error 回滚
// 并原样透传，panic 会回滚并重新 panic——不要在 fn 外面自己加 recover
// 绕过这个语义。
func InTxWithOptions(ctx context.Context, db DB, opts *sql.TxOptions, fn func(context.Context) error) error {
	if fn == nil {
		return errNilTxFn
	}
	ctx = normalizeContext(ctx)
	if txFromContext(ctx) != nil {
		return fn(ctx)
	}
	if db == nil {
		return errNilDB
	}
	txOpts, err := toPgxTxOptions(opts)
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, db, txOpts, func(tx pgx.Tx) error {
		return fn(WithTx(ctx, tx))
	})
}

// TxManager 把 InTx / InTxWithOptions 绑定到一个 DB 上，让 service 依赖
// service.Transactor 接口而不是直接依赖连接池。
type TxManager struct {
	db DB
}

// NewTxManager 构造 TxManager；由 internal/server.go 装配。
func NewTxManager(db DB) *TxManager {
	return &TxManager{db: db}
}

// InTx 在事务里执行 fn；语义同包级 InTx。
func (m *TxManager) InTx(ctx context.Context, fn func(context.Context) error) error {
	return InTx(ctx, m.db, fn)
}

// InTxWithOptions 用自定义 isolation 执行 fn；语义同包级 InTxWithOptions。
func (m *TxManager) InTxWithOptions(ctx context.Context, opts *sql.TxOptions, fn func(context.Context) error) error {
	return InTxWithOptions(ctx, m.db, opts, fn)
}

// toPgxTxOptions 把 database/sql 的选项映射成 pgx 的选项。Postgres 不支持
// 的 isolation level 直接报错，而不是悄悄降级成别的语义。
func toPgxTxOptions(opts *sql.TxOptions) (pgx.TxOptions, error) {
	var out pgx.TxOptions
	if opts == nil {
		return out, nil
	}
	if opts.ReadOnly {
		out.AccessMode = pgx.ReadOnly
	}
	switch opts.Isolation {
	case sql.LevelDefault:
	case sql.LevelReadUncommitted:
		out.IsoLevel = pgx.ReadUncommitted
	case sql.LevelReadCommitted:
		out.IsoLevel = pgx.ReadCommitted
	case sql.LevelRepeatableRead:
		out.IsoLevel = pgx.RepeatableRead
	case sql.LevelSerializable:
		out.IsoLevel = pgx.Serializable
	default:
		return pgx.TxOptions{}, fmt.Errorf("repository: unsupported isolation level %s", opts.Isolation)
	}
	return out, nil
}

// dbFromContext 返回 ctx 里挂的事务句柄，没有就返回 db。所有 repository
// 方法都必须走这一层，否则 InTx 包住的调用链会绕过事务，用错连接。
func dbFromContext(ctx context.Context, db DB) sqlcdb.DBTX {
	if tx := txFromContext(ctx); tx != nil {
		return tx
	}
	return db
}

// txFromContext 从 ctx 里取活跃事务句柄；没有返回 nil。
func txFromContext(ctx context.Context) sqlcdb.DBTX {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value(txKey{}).(sqlcdb.DBTX)
	return tx
}

// normalizeContext 把 nil ctx 兜底成 context.Background，防止上游误传 nil
// 直接炸到驱动层。
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
