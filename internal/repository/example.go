package repository

// Example repository 教学模板：repository 是**唯一**允许写 GORM 的层
//
//   - 所有查询都用 dbFromContext(ctx, r.db).WithContext(ctx)，让 trace_id /
//     超时 / 取消信号一路传下去，**不要**用 context.Background() 替换。
//   - 事务用 InTx(ctx, db, fn) + dbFromContext(ctx, r.db)；跨 repository
//     事务边界由 service 决定，单 repository 读一致性可以在本层用只读事务兜住。
//   - service 通过包内定义的 ExampleRepository 接口依赖本类型，不要 export
//     gorm.io/gorm 给 service 看到。
//
// 集成测试模板见 example_integration_test.go（//go:build integration），
// 跑 make test-integration 触发。

import (
	"context"
	"database/sql"

	"gorm.io/gorm"

	"go-skeleton/internal/model"
)

// ExampleRepository 落 example 数据。持有默认 *gorm.DB；事务里通过
// dbFromContext 切换到 ctx 上挂的事务句柄。
type ExampleRepository struct {
	db *gorm.DB
}

// NewExampleRepository 构造 ExampleRepository。db 由 internal/server.go 装配。
func NewExampleRepository(db *gorm.DB) *ExampleRepository {
	return &ExampleRepository{db: db}
}

// Create 插一条 example，并把生成的 ID / 时间戳回填到入参 example 上。
// 走 dbFromContext 让事务里嵌套调用复用同一条事务连接。
func (r *ExampleRepository) Create(ctx context.Context, example *model.Example) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Create(example).Error
}

// List 按 id DESC 返回分页 example 列表 + 总行数。Count 和 Find 在同一个
// RepeatableRead + ReadOnly 事务里执行，保证 total 和 rows 来自同一快照。
func (r *ExampleRepository) List(ctx context.Context, limit, offset int) ([]model.Example, int64, error) {
	var total int64
	var examples []model.Example

	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	err := InTxWithOptions(ctx, r.db, opts, func(txCtx context.Context) error {
		db := dbFromContext(txCtx, r.db).WithContext(txCtx)
		if err := db.Model(&model.Example{}).Count(&total).Error; err != nil {
			return err
		}
		return db.Order("id DESC").Limit(limit).Offset(offset).Find(&examples).Error
	})
	if err != nil {
		return nil, 0, err
	}

	return examples, total, nil
}
