//go:build integration

// 集成测试示例：连真实 Postgres，验证 sqlc 查询行为。
//
// 触发方式：
//
//	make dev-up                         # 起本地 Postgres + Redis
//	go run ./cmd/migrate                # 建表
//	make test-integration               # 跑所有 //go:build integration 的测试
//
// 单元测试默认不跑这一档（make test / go test ./... 都跳过），
// CI 也只跑单元测试，所以本文件不会拖慢日常迭代。

package repository_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-skeleton/internal/model"
	"go-skeleton/internal/repository"
)

// openTestPool 从 POSTGRES env 取 DSN；没配就跳过，避免 CI / 本地误触发时
// 直接 fail。
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES")
	if dsn == "" {
		t.Skip("POSTGRES env not set; skipping integration test")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestExampleRepositoryIntegration_CreateThenList(t *testing.T) {
	pool := openTestPool(t)
	repo := repository.NewExampleRepository(pool)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	name := "integration-" + time.Now().Format("150405.000")
	e := &model.Example{Name: name}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.ID == 0 {
		t.Fatalf("expected ID populated after create")
	}

	rows, total, err := repo.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total < 1 {
		t.Fatalf("expected total >= 1, got %d", total)
	}

	var found bool
	for _, r := range rows {
		if r.ID == e.ID && r.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created row %d not in first page", e.ID)
	}
}
