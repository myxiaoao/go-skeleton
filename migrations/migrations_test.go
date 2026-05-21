package migrations

import (
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"testing"
)

// TestMigrationsAreValid 静态校验所有 embed 的迁移文件：文件名版本号（首段数字）
// 可解析、严格递增无重复，且每个文件都带 `-- +goose Up` 与 `-- +goose Down` 注解。
// 不连数据库、不依赖 goose 的 Provider（它强制要 *sql.DB）——纯读 embed FS，能在
// make verify 里挡住 "迁移文件写坏 / 版本号撞车 / 漏写 goose 注解" 这类低级错误。
// 注：这里只校验注解存在，Down 段写得对不对要到真实回滚才暴露——生产回滚优先靠
// pg_dump 备份而非 down，见 docs/deploy.md §5.2。
func TestMigrationsAreValid(t *testing.T) {
	names, err := fs.Glob(FS, "*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no *.sql migrations found in embed FS; expected at least the initial schema")
	}

	var prev int64
	for _, name := range names {
		version, err := parseVersion(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if version <= prev {
			t.Errorf("%s: version %d not strictly increasing (prev=%d)", name, version, prev)
		}
		prev = version

		body, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(body), "-- +goose Up") {
			t.Errorf("%s: missing `-- +goose Up` annotation", name)
		}
		if !strings.Contains(string(body), "-- +goose Down") {
			t.Errorf("%s: missing `-- +goose Down` annotation", name)
		}
	}
}

// parseVersion 取文件名首个 `_` 前的数字段作版本号（goose 约定）。
func parseVersion(name string) (int64, error) {
	base := path.Base(name)
	i := strings.IndexByte(base, '_')
	if i <= 0 {
		return 0, fmt.Errorf("filename must be `<version>_<desc>.sql`")
	}
	v, err := strconv.ParseInt(base[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("version prefix %q is not numeric: %w", base[:i], err)
	}
	return v, nil
}
