package repository

// Example repository 教学模板：repository 是**唯一**允许写 GORM 的层
//
//   - 所有查询都用 dbFromContext(ctx, r.db).WithContext(ctx)，让 trace_id /
//     超时 / 取消信号一路传下去，**不要**用 context.Background() 替换。
//   - 事务用 InTx(ctx, db, fn) + dbFromContext(ctx, r.db)，事务边界由 service
//     决定，repository 只负责被复用。
//   - service 通过包内定义的 ExampleRepository 接口依赖本类型，不要 export
//     gorm.io/gorm 给 service 看到。
//
// 集成测试模板见 example_integration_test.go（//go:build integration），
// 跑 make test-integration 触发。

import (
	"context"

	"gorm.io/gorm"

	"go-skeleton/internal/model"
)

// ExampleRepository persists examples.
type ExampleRepository struct {
	db *gorm.DB
}

// NewExampleRepository creates an ExampleRepository.
func NewExampleRepository(db *gorm.DB) *ExampleRepository {
	return &ExampleRepository{db: db}
}

// Create stores an example.
func (r *ExampleRepository) Create(ctx context.Context, example *model.Example) error {
	return dbFromContext(ctx, r.db).WithContext(ctx).Create(example).Error
}

// List returns examples ordered by newest first plus the total row count.
//
// **快照一致性说明：** Count 和 Find 是两条独立查询，在 PostgreSQL 默认的
// `READ COMMITTED` 隔离级别下，两条查询之间发生的写入会让返回值不一致
// （total 可能 +1 但 rows 不含该新增项，或反过来）。total 因此是"近似值"，
// 用于分页 UI 足够；若业务需要强一致 total（如按 total 做分页边界判断），
// 调用方应该用 InTx + `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ` 包住。
func (r *ExampleRepository) List(ctx context.Context, limit, offset int) ([]model.Example, int64, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var total int64
	if err := db.Model(&model.Example{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var examples []model.Example
	if err := db.Order("id DESC").Limit(limit).Offset(offset).Find(&examples).Error; err != nil {
		return nil, 0, err
	}

	return examples, total, nil
}
