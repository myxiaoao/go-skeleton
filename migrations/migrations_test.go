package migrations

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
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

// filenameRe 强制 `<14位 UTC 时间戳>_<snake_case 描述>.sql`，对齐
// `make migrate-create` 的产出格式（CLAUDE.md / AGENTS.md "顶层目录"
// 段约定）。把命名漂移挡在 commit 之前，避免：
//   - YYYYMMDD（少时分秒）→ 同一天多次迁移撞版本号
//   - 时间戳中插下划线 → goose parseVersion 取首段数字失败
//   - 描述带大写 / 连字符 / 空格 → 跨工具脚本（make / sed / shell）兼容性差
var filenameRe = regexp.MustCompile(`^[0-9]{14}_[a-z0-9_]+\.sql$`)

// TestMigrationsFilenameFormat 单独走一个 test 报错位置更准（filename 错和
// version 不递增是两类问题，混在一起堆 Errorf 看不清）。
func TestMigrationsFilenameFormat(t *testing.T) {
	names, err := fs.Glob(FS, "*.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, name := range names {
		if !filenameRe.MatchString(name) {
			t.Errorf("%s: filename must match `<YYYYMMDDHHMMSS>_<snake_case>.sql` (got %q)", name, name)
		}
	}
}

// dangerousDDL 罗列"破坏向后兼容"的 DDL 形态。匹配是行级正则、忽略大小
// 写。命中后必须在文件里加 `-- breaking: <reason>` 显式标注，把 expand-
// contract 决策从"代码 review 拍脑袋"提升成"commit 时必须自证"。
//
// 这里只检 Up 段命中——Down 段几乎一定包含反向 DDL（CREATE TABLE 的 Down
// 就是 DROP TABLE），那是设计内的、不算 breaking。
var dangerousDDL = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*drop\s+table\b`),
	regexp.MustCompile(`(?i)^\s*alter\s+table\s+\S+\s+drop\s+(column|constraint)\b`),
	regexp.MustCompile(`(?i)^\s*alter\s+table\s+\S+\s+alter\s+(column\s+)?\S+\s+(type|set\s+not\s+null)\b`),
	regexp.MustCompile(`(?i)^\s*alter\s+table\s+\S+\s+rename\s+(column|to)\b`),
	regexp.MustCompile(`(?i)^\s*truncate\b`),
}

// breakingMarkerRe 允许两种写法：
//
//	`-- breaking: 某理由`
//	`-- +breaking 某理由`（喜欢与 goose pragma 风格对齐的开发者）
var breakingMarkerRe = regexp.MustCompile(`(?im)^\s*--\s*\+?breaking[:\s]`)

// TestMigrationsBreakingDDLMustBeMarked 强制破坏性 DDL 显式标注。挡住"无
// 标注就上 DROP COLUMN"这种回滚困难的提交。如果确实需要 expand-contract，
// 在 Up 段开头加：
//
//	-- breaking: 配合 v2.4 服务下线，old_email 列已无引用方
//	ALTER TABLE users DROP COLUMN old_email;
//
// 配合 docs/deploy.md §5 的升级 / 回滚段使用。
func TestMigrationsBreakingDDLMustBeMarked(t *testing.T) {
	names, err := fs.Glob(FS, "*.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, name := range names {
		body, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		upSection := extractUpSection(string(body))
		marked := breakingMarkerRe.MatchString(upSection)
		for line := range strings.SplitSeq(upSection, "\n") {
			// 去掉行尾 `;` 让 ALTER ... DROP CONSTRAINT 这种带分号的匹配也走得通。
			trimmed := strings.TrimRight(line, "; \t")
			for _, re := range dangerousDDL {
				if re.MatchString(trimmed) && !marked {
					t.Errorf("%s: dangerous DDL detected without `-- breaking: <reason>` marker:\n  %s\n  Add the marker in the Up section or split into expand-contract migrations.",
						name, strings.TrimSpace(line))
				}
			}
		}
	}
}

// extractUpSection 抠出 `-- +goose Up` 与 `-- +goose Down`（或文件结尾）
// 之间的内容；找不到 Up 时返空串，让上层 TestMigrationsAreValid 报漏注解。
func extractUpSection(body string) string {
	up := strings.Index(body, "-- +goose Up")
	if up < 0 {
		return ""
	}
	rest := body[up:]
	if down := strings.Index(rest, "-- +goose Down"); down >= 0 {
		return rest[:down]
	}
	return rest
}

// TestMigrationsBreakingDDLDetectionLogic 是 dangerousDDL / breakingMarkerRe
// 的反向用例，确保正则真能拦下来——只校验已 embed 的迁移文件不够，因为
// 现仓库里没有破坏性 DDL（也不希望有），无法反向验证正则正确。
//
// 表里"want hit"= 该 DDL 应被 dangerousDDL 匹配。
// "want marked"= 配上 marker 后能被 breakingMarkerRe 识别。
func TestMigrationsBreakingDDLDetectionLogic(t *testing.T) {
	cases := []struct {
		name string
		stmt string
		want bool
	}{
		{"drop table", "DROP TABLE users;", true},
		{"drop column", "ALTER TABLE users DROP COLUMN old_email;", true},
		{"drop constraint", "ALTER TABLE users DROP CONSTRAINT fk_org;", true},
		{"alter column type", "ALTER TABLE users ALTER COLUMN age TYPE INT;", true},
		{"alter column set not null", "ALTER TABLE users ALTER COLUMN email SET NOT NULL;", true},
		{"rename column", "ALTER TABLE users RENAME COLUMN old TO new;", true},
		{"rename table", "ALTER TABLE users RENAME TO accounts;", true},
		{"truncate", "TRUNCATE TABLE users;", true},
		{"create table benign", "CREATE TABLE users (id BIGSERIAL PRIMARY KEY);", false},
		{"add column benign", "ALTER TABLE users ADD COLUMN email VARCHAR(255);", false},
		{"create index benign", "CREATE INDEX idx_users_email ON users(email);", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trimmed := strings.TrimRight(tc.stmt, "; \t")
			hit := false
			for _, re := range dangerousDDL {
				if re.MatchString(trimmed) {
					hit = true
					break
				}
			}
			if hit != tc.want {
				t.Fatalf("dangerousDDL.match(%q) = %v, want %v", tc.stmt, hit, tc.want)
			}
		})
	}

	// breaking marker 接受两种写法。
	for _, line := range []string{
		"-- breaking: 配合 v2.4 服务下线",
		"-- +breaking 配合 v2.4 服务下线",
		"--breaking: tight spacing",
	} {
		if !breakingMarkerRe.MatchString(line) {
			t.Errorf("breakingMarkerRe failed to match: %q", line)
		}
	}
	for _, line := range []string{
		"-- comment about breaking change",
		"DROP TABLE users; -- not a marker",
	} {
		if breakingMarkerRe.MatchString(line) {
			t.Errorf("breakingMarkerRe matched non-marker line: %q", line)
		}
	}
}
