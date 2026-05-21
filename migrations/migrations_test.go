package migrations

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationsAreCollectible 静态校验所有 embed 的迁移文件：能被 goose 解析、
// 版本号严格递增且无重复。不连数据库，只读 embed FS——能在 make verify 里挡住
// "迁移文件写坏 / 版本号撞车" 这类低级错误，不依赖跑起一个真实库。
func TestMigrationsAreCollectible(t *testing.T) {
	goose.SetBaseFS(FS)
	t.Cleanup(func() { goose.SetBaseFS(nil) })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}

	// current=0, target=max：收集全部迁移。
	got, err := goose.CollectMigrations(".", 0, int64(1)<<62)
	if err != nil {
		t.Fatalf("collect migrations: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no migrations found in embed FS; expected at least the initial schema")
	}

	var prev int64
	for _, m := range got {
		if m.Version <= prev {
			t.Errorf("migration version %d not strictly increasing (prev=%d, file=%s)", m.Version, prev, m.Source)
		}
		prev = m.Version
	}
}
