package repository

// Example repository 教学模板：repository 是**唯一**允许写 SQL 的层。
//
//   - SQL 写在 queries/example.sql；`make sqlc` 生成 package sqlcdb。本文件
//     只负责挑连接、调生成的方法、把行映射成 model 类型。
//   - 都走 dbFromContext(ctx, r.db)，让 InTx 包住的调用复用同一条事务；
//     ctx 原样传下去，不要用 context.Background() 替换。
//   - sqlcdb 的行类型只留在本包内部，经 toExampleModel 转成 model.Example
//     再往外传，不要让上层看到 sqlcdb 的类型。
//
// 集成测试模板见 example_integration_test.go（//go:build integration），
// 跑 make test-integration 触发。

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"

	"go-skeleton/internal/model"
	"go-skeleton/internal/repository/sqlcdb"
)

// ExampleRepository 落 example 数据。持有默认 DB（*pgxpool.Pool）；事务里
// 通过 dbFromContext 切换到 ctx 上挂的事务句柄。
type ExampleRepository struct {
	db DB
}

// NewExampleRepository 构造 ExampleRepository。db 由 internal/server.go 装配
// （*pgxpool.Pool）。
func NewExampleRepository(db DB) *ExampleRepository {
	return &ExampleRepository{db: db}
}

// Create 插一条 example，并用 RETURNING 回填 ID / 时间戳到入参 example 上。
func (r *ExampleRepository) Create(ctx context.Context, example *model.Example) error {
	row, err := sqlcdb.New(dbFromContext(ctx, r.db)).CreateExample(ctx, example.Name)
	if err != nil {
		return err
	}
	*example = toExampleModel(exampleRow(row))
	return nil
}

// CreateWithTaskKey 带幂等键插一条 example：taskKey 已存在（同一任务重复执行）
// 时不插入，返回 created=false、err=nil；插入成功时回填 ID / 时间戳到 example。
func (r *ExampleRepository) CreateWithTaskKey(ctx context.Context, example *model.Example, taskKey string) (bool, error) {
	row, err := sqlcdb.New(dbFromContext(ctx, r.db)).CreateExampleWithTaskKey(ctx, sqlcdb.CreateExampleWithTaskKeyParams{
		Name:    example.Name,
		TaskKey: taskKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING 命中时 RETURNING 不返回行。
		return false, nil
	}
	if err != nil {
		return false, err
	}
	*example = toExampleModel(exampleRow(row))
	return true, nil
}

// List 按 id DESC 返回分页 example 列表 + 总行数。Count 和 List 在同一个
// RepeatableRead + ReadOnly 事务里执行，保证 total 和 rows 来自同一快照。
func (r *ExampleRepository) List(ctx context.Context, limit, offset int) ([]model.Example, int64, error) {
	var (
		total    int64
		examples []model.Example
	)
	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	err := InTxWithOptions(ctx, r.db, opts, func(txCtx context.Context) error {
		q := sqlcdb.New(dbFromContext(txCtx, r.db))
		n, err := q.CountExamples(txCtx)
		if err != nil {
			return err
		}
		// sqlc 生成的 ListExamples 把 OFFSET 绑成 $1（Off）、LIMIT 绑成 $2
		// （Lim）；这里用具名字段传参，调用方不需要关心绑定顺序。
		rows, err := q.ListExamples(txCtx, sqlcdb.ListExamplesParams{Lim: int64(limit), Off: int64(offset)})
		if err != nil {
			return err
		}
		total = n
		examples = make([]model.Example, 0, len(rows))
		for _, row := range rows {
			examples = append(examples, toExampleModel(exampleRow(row)))
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return examples, total, nil
}

// exampleRow 是各查询共用的行形状（id, name, created_at, updated_at）。sqlc
// 为每个查询各生成一个字段完全相同的 XxxRow 类型，Go 允许它们之间直接做类型
// 转换，统一转成 exampleRow 后复用同一个映射函数。
type exampleRow sqlcdb.ListExamplesRow

// toExampleModel 把 sqlc 生成的行类型映射成对外的 model.Example。
func toExampleModel(row exampleRow) model.Example {
	return model.Example{
		ID:        row.ID,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}
