//go:build ignore

// drop-example 把骨架里的 Example 示例模块整体拔掉。
//
// 入口：
//
//	go run scripts/drop-example.go      # 直接跑
//	make drop-example                   # 推荐
//
// 跑完后仓库里**不再有** Example 业务模块（handler / service / repository /
// model / task / 装配 / 路由 / openapi 契约 / 数据库迁移），但骨架机制
// （bootstrap / oapi-codegen / 锚点 / verify 链）全保留——可以直接
// `make new-endpoint NAME=<Name>` 起真业务（注意 new-endpoint 模板依赖
// example.go，drop 后要先 git checkout 历史版本恢复模板，或先 git revert）。
//
// 实现要点：
//   - Go AST 删字段需引入 dave/dst 才能保留注释 / 空行，引第三方依赖只为
//     一个一次性脚本不划算。所以 .go 文件改动走"精确多行字符串替换"，每
//     步先 strings.Contains 校验存在再 Replace，缺即跳（幂等），最后跑
//     `gofmt -e` 校验语法不破。
//   - openapi.yaml 走相同思路：按文本块定位（key 唯一，缩进固定），最后
//     折叠多余空行。不解析整个 yaml——yaml.v3 Marshal 会丢失注释。
//   - 安全网：拒绝 dirty checkout、每步幂等、跑完调 `make oapi && make verify`，
//     失败 fail-fast。
//
// 不属于任何包，//go:build ignore 让 go build/test 跳过它（与 scripts/gen-errcodes.go 一致）。
package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	root, err := repoRoot()
	must(err, "locate repo root")
	must(os.Chdir(root), "chdir to repo root")

	must(ensureCleanCheckout(), "checkout cleanliness")

	log.Println("drop-example: 开始拔示例模块...")

	must(deleteFiles(filesToDelete()), "delete layer files")
	must(ensureMigrationsPlaceholder(), "leave migrations placeholder")
	must(patchServerGo(), "patch internal/server.go")
	must(patchRouterGo(), "patch internal/router/router.go")
	must(patchWorkerGo(), "patch internal/worker.go")
	must(rewriteWorkerHandler(), "rewrite internal/worker/handler.go")
	must(patchOpenAPIHandler(), "patch internal/handler/openapi.go")
	must(patchTxHelpers(), "silence unused tx helpers")
	must(patchCrossPackageTests(), "patch cross-package tests")
	must(patchOpenAPIYAML(), "patch api/openapi.yaml")

	must(validateGoSyntax([]string{
		"internal/server.go",
		"internal/router/router.go",
		"internal/worker.go",
		"internal/worker/handler.go",
		"internal/handler/openapi.go",
		"internal/server_test.go",
		"internal/router/router_test.go",
		"internal/handler/openapi_test.go",
	}), "go syntax check")

	must(runMake("oapi"), "make oapi")
	must(gofmtAll(), "gofmt")
	must(goImportsAll(), "goimports (best-effort)")
	// 删 Example schemas 后 oapi.gen.go 可能不再 import 某些 runtime 包，
	// 跑 go mod tidy 把 go.mod / go.sum 收敛——tidy-verify 是 verify 链
	// 的常驻门禁，drop-example 跑完不 tidy 会卡在 verify 末尾。
	must(runGo("mod", "tidy"), "go mod tidy")

	log.Println("drop-example: 跑构建 + 测试 + 静态校验确认改动正确...")
	// 不跑完整 make verify——里头的 oapi-verify / docs-deploy-check /
	// docs-errcodes-verify 都用 `git diff --quiet` 对比工作树和 HEAD，
	// drop-example 改了生成产物但还没让用户提交，必然 "out of sync"。
	// 这里跑构成 verify 的真材实料子集：fmt / vet / test / lint /
	// architecture / env / tidy / docs-verify（这些不依赖 HEAD ↔ 工作树
	// diff）。"与 HEAD 同步" 类校验由用户 commit 后再跑 make verify 自查。
	subverify := []string{
		"fmt",
		"vet",
		"test",
		"lint",
		"architecture-verify",
		"env-verify",
		"tidy-verify",
		"docs-verify",
	}
	for _, t := range subverify {
		if err := runMake(t); err != nil {
			fmt.Fprintf(os.Stderr, `
drop-example: make %s 失败。最常见的剩余清理：
  - 还有 _test.go 引用了已删的 Example 类型（grep -rn ExampleHandler internal/）
  - new-endpoint 模板依赖已删 example.go——要新增模块需从 git 历史恢复模板
    或先 git revert drop-example。
`, t)
			os.Exit(1)
		}
	}

	fmt.Println(`
✅ Example 示例模块已拔除（fmt/vet/test/lint/architecture/env/tidy/docs-verify 全绿）。
   后续动作：
   1. git status / git diff 确认改动符合预期
   2. CHANGELOG.md 写一条 Removed：移除示例 Example 模块
   3. git add -A && git commit
   4. commit 后再跑一次 make verify：oapi-verify / docs-deploy-check /
      docs-errcodes-verify 比对工作树和 HEAD，要等本次改动入库后才会绿。
   5. 起真业务：make new-endpoint NAME=<Name>
      （drop-example 跑过后 new-endpoint 模板依赖的 example.go 已不存在，
      需从 git 历史 checkout 模板，或先 git revert 本次提交。）`)
}

// ---------------------------------------------------------------------------
// 工具

func must(err error, what string) {
	if err != nil {
		log.Fatalf("drop-example: %s: %v", what, err)
	}
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func ensureCleanCheckout() error {
	if err := exec.Command("git", "diff", "--quiet", "HEAD").Run(); err != nil {
		return fmt.Errorf("工作区有未提交改动，先 commit 或 stash（脚本会改多个文件，dirty checkout 下出错难回滚）")
	}
	if err := exec.Command("git", "diff", "--cached", "--quiet").Run(); err != nil {
		return fmt.Errorf("暂存区非空，先 commit")
	}
	return nil
}

// ensureMigrationsPlaceholder 给 migrations/ 留个最小占位 .sql。
//
// 缘由：migrations/embed.go 用 `//go:embed *.sql`，glob 无匹配时整个包
// 编译失败（package-level error，不是运行期 fs.ErrNotExist）。删掉示例
// 迁移后必须留一份 .sql 让 embed glob 有目标。
//
// 占位文件名以 `0001` 起头，迁移文件名 lint 需要 14 位时间戳，所以选
// 一个合规且明显是占位的串：`00010101000001_placeholder.sql`。开发者
// 接入第一条真业务迁移时（make migrate-create + 写 SQL）应该删掉这个
// 占位——脚本输出会提示这一点。
//
// Up/Down 段都用 `SELECT 1;`：goose 接受、对 DB 零副作用。
func ensureMigrationsPlaceholder() error {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			return nil // 已有 .sql（重跑 / 用户手写过），不再插占位。
		}
	}
	const path = "migrations/00010101000001_placeholder.sql"
	const body = `-- +goose Up
-- 这是 drop-example 留下的占位迁移：让 migrations/embed.go 的 //go:embed *.sql
-- 至少匹配到一份文件，避免 package-level 编译失败。
-- 接入第一条真业务迁移时：
--   1. 跑 make migrate-create name=<your_first_migration>
--   2. 删掉本占位文件
SELECT 1;

-- +goose Down
SELECT 1;
`
	if err := writeFile(path, body); err != nil {
		return err
	}
	log.Printf("  ✓ left migrations placeholder %s (接入真业务后删它)", path)
	return nil
}

func deleteFiles(paths []string) error {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("rm %s: %w", p, err)
			}
			log.Printf("  ✓ deleted %s", p)
		}
	}
	return nil
}

func filesToDelete() []string {
	return []string{
		"internal/handler/example.go",
		"internal/handler/example_test.go",
		"internal/service/example.go",
		"internal/service/example_test.go",
		"internal/repository/example.go",
		"internal/repository/example_test.go",
		"internal/repository/example_integration_test.go",
		"internal/model/example.go",
		"internal/model/example_test.go",
		"internal/task/example.go",
		"internal/worker/handler_test.go",
		"migrations/20260521000001_create_examples_table.sql",
	}
}

// readFile / writeFile / replaceAll: 给 patch 步骤的薄包装。
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// replaceBlock 在 path 里把 old 替换成 new。old 必须唯一出现一次，找不到
// 视为已删（跳过），找到多处视为模板漂移（错）。每个 patch 单调用一次，
// log 一条便于审计。
func replaceBlock(path, desc, old, new string) error {
	content, err := readFile(path)
	if err != nil {
		return err
	}
	count := strings.Count(content, old)
	switch count {
	case 0:
		log.Printf("  · skip %s (%s: pattern absent — already dropped)", path, desc)
		return nil
	case 1:
		// 正常
	default:
		return fmt.Errorf("%s: %s: pattern matched %d times, expected exactly 1 (refusing to risk ambiguous replace)", path, desc, count)
	}
	if err := writeFile(path, strings.Replace(content, old, new, 1)); err != nil {
		return err
	}
	log.Printf("  ✓ patched %s (%s)", path, desc)
	return nil
}

// ---------------------------------------------------------------------------
// internal/server.go

func patchServerGo() error {
	const path = "internal/server.go"
	patches := []struct{ desc, old, new string }{
		{
			"drop HTTPHandlers.Example field",
			"\tExample *handler.ExampleHandler\n\t// NEH handlers-fields",
			"\t// NEH handlers-fields",
		},
		{
			"drop repository + service construction",
			"\texampleRepository := repository.NewExampleRepository(db)\n" +
				"\texampleService := service.NewExampleService(exampleRepository, reg.Queue)\n" +
				"\t// NEH handlers-deps",
			"\t// NEH handlers-deps",
		},
		{
			"drop handler construction",
			"\texampleH := handler.NewExampleHandler(exampleService)\n" +
				"\t// NEH handlers-construct",
			"\t// NEH handlers-construct",
		},
		{
			"drop HTTPHandlers return literal",
			"\t\tExample: exampleH,\n\t\t// NEH handlers-return",
			"\t\t// NEH handlers-return",
		},
		{
			"drop APIServer literal Example",
			"\t\tAPI: &handler.APIServer{\n" +
				"\t\t\tAuth:    authH,\n" +
				"\t\t\tHealth:  healthH,\n" +
				"\t\t\tExample: exampleH,\n" +
				"\t\t\tOpenAPI: openapiH,\n" +
				"\t\t},",
			"\t\tAPI: &handler.APIServer{\n" +
				"\t\t\tAuth:    authH,\n" +
				"\t\t\tHealth:  healthH,\n" +
				"\t\t\tOpenAPI: openapiH,\n" +
				"\t\t},",
		},
		{
			"drop RegisterRoutes Example dep",
			"\tif err := router.RegisterRoutes(api, router.Dependencies{\n" +
				"\t\tAuth:         handlers.Auth,\n" +
				"\t\tAuthRequired: authRequired,\n" +
				"\t\tExample:      handlers.Example,\n" +
				"\t}); err != nil {",
			"\tif err := router.RegisterRoutes(api, router.Dependencies{\n" +
				"\t\tAuth:         handlers.Auth,\n" +
				"\t\tAuthRequired: authRequired,\n" +
				"\t}); err != nil {",
		},
		{
			// `db := reg.DB.DB()` 在 newHTTPHandlers 里——示例装配是它的
			// 唯一用户。删完 Example 后变成 "declared and not used"。改成
			// 留 `db := ...` + `_ = db` 占位，让 new-endpoint 注入的
			// repository.New<Name>Repository(db) 仍能拿到 db 句柄，无须
			// 改脚手架脚本；新增模块后开发者可手动删 `_ = db`。
			"silence unused db var after Example removal",
			"func newHTTPHandlers(reg *bootstrap.Registry) *HTTPHandlers {\n" +
				"\tdb := reg.DB.DB()\n" +
				"\t// NEH handlers-deps",
			"func newHTTPHandlers(reg *bootstrap.Registry) *HTTPHandlers {\n" +
				"\t// db 保留给 new-endpoint 注入的 repository.New<Name>Repository(db)；\n" +
				"\t// 当前没有业务模块时用 _ = db 静音 \"declared and not used\"。\n" +
				"\t// 新增模块后删掉下一行。\n" +
				"\tdb := reg.DB.DB()\n" +
				"\t_ = db\n" +
				"\t// NEH handlers-deps",
		},
	}
	for _, p := range patches {
		if err := replaceBlock(path, p.desc, p.old, p.new); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// internal/router/router.go

func patchRouterGo() error {
	const path = "internal/router/router.go"
	patches := []struct{ desc, old, new string }{
		{
			"drop Dependencies.Example",
			"\tExample      *handler.ExampleHandler\n\t// NEH deps-fields",
			"\t// NEH deps-fields",
		},
		{
			"drop registerExampleRoutes call",
			"\tregisterExampleRoutes(r, deps)\n\t// NEH routes-register",
			"\t// NEH routes-register",
		},
	}
	for _, p := range patches {
		if err := replaceBlock(path, p.desc, p.old, p.new); err != nil {
			return err
		}
	}

	// registerExampleRoutes 函数体：尾巴上一整段，用 regex 删（保留前后空行规整）。
	content, err := readFile(path)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?s)\n// registerExampleRoutes 挂.*?\nfunc registerExampleRoutes\(r \*gin\.RouterGroup, deps Dependencies\) \{.*?\n\}\n`)
	if !re.MatchString(content) {
		log.Printf("  · skip %s (registerExampleRoutes func absent — already dropped)", path)
		return nil
	}
	content = re.ReplaceAllString(content, "\n")
	// 折叠 3 个以上连续换行
	content = regexp.MustCompile(`\n{3,}`).ReplaceAllString(content, "\n\n")
	if err := writeFile(path, content); err != nil {
		return err
	}
	log.Printf("  ✓ patched %s (drop registerExampleRoutes func)", path)
	return nil
}

// ---------------------------------------------------------------------------
// internal/worker.go

func patchWorkerGo() error {
	const path = "internal/worker.go"
	const oldBlock = `// buildWorkerDeps 把 Registry 翻译成 worker handler 用的 Deps。
//
// Example processor 走 typed contract：reg.DB 可用时注入真 ExampleService
// （走 repository → gorm 落库），DB 不可用时让 RegisterHandlers 回填
// noopExampleProcessor 兜底，便于无 DB 的 worker 部署形态（如只跑外部 API
// 任务）也能起得来。worker 包本身不 import gorm，符合分层规则。
func buildWorkerDeps(reg *bootstrap.Registry) *worker.Deps {
	deps := &worker.Deps{
		Cache: reg.Cache,
		Queue: reg.Queue,
	}
	if reg.DB != nil {
		repo := repository.NewExampleRepository(reg.DB.DB())
		deps.Example = service.NewExampleService(repo, reg.Queue)
	}
	return deps
}
`
	const newBlock = `// buildWorkerDeps 把 Registry 翻译成 worker handler 用的 Deps。
// 真实业务自行在这里把对应 service 注入 Deps 的具体字段。
func buildWorkerDeps(reg *bootstrap.Registry) *worker.Deps {
	return &worker.Deps{
		Cache: reg.Cache,
		Queue: reg.Queue,
	}
}
`
	return replaceBlock(path, "drop buildWorkerDeps Example wiring", oldBlock, newBlock)
}

// ---------------------------------------------------------------------------
// internal/worker/handler.go — 整文件重写

func rewriteWorkerHandler() error {
	const path = "internal/worker/handler.go"
	const body = `package worker

import (
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"go-skeleton/internal/taskqueue"
	"go-skeleton/pkg/cache"
)

// Deps 收拢所有异步任务 handler 共用的依赖。
//
// 故意**不**包含 *gorm.DB：repository 是项目里唯一允许 import gorm 的层
// （见 CLAUDE.md 分层规则）。Worker handler 需要落库的话，走 service 接口
// → repository → gorm，而不是在 worker 包内直接拿 *gorm.DB。
//
// Cache / RDB / Queue 是 pkg/ 通用工具，worker import 它们不破坏分层。
// 业务接入新任务时按 CLAUDE.md "异步队列" 段，在本 struct 上加 typed
// processor 接口字段，避免回退到 interface{}。
type Deps struct {
	Cache *cache.Client
	RDB   *redis.Client
	Queue *taskqueue.Queue
}

// RegisterHandlers 把所有异步任务 handler 注册到 mux 上。注册 TraceMiddleware
// 让 task 调用链自带 trace_id；deps 为 nil 兜底成空 Deps，让 mux 仍然可用。
//
// 业务接入流程见 CLAUDE.md "异步队列" 段：定义 payload + Processor 接口 +
// HandleXxxTask + 在这里 mux.HandleFunc(task.TypeXxx, deps.HandleXxxTask)。
func RegisterHandlers(mux *asynq.ServeMux, deps *Deps) {
	if mux == nil {
		return
	}
	registerTraceMiddleware(mux)
	if deps == nil {
		deps = &Deps{}
	}
	_ = deps
}
`
	if err := writeFile(path, body); err != nil {
		return err
	}
	log.Printf("  ✓ rewrote %s (drop Example processor + handler)", path)
	return nil
}

// ---------------------------------------------------------------------------
// internal/handler/openapi.go

func patchOpenAPIHandler() error {
	const path = "internal/handler/openapi.go"
	if err := replaceBlock(path,
		"drop APIServer.Example field",
		"\tHealth  *HealthHandler\n\tExample *ExampleHandler\n\tOpenAPI *OpenAPIHandler\n",
		"\tHealth  *HealthHandler\n\tOpenAPI *OpenAPIHandler\n",
	); err != nil {
		return err
	}

	return replaceBlock(path,
		"drop ListExamples / CreateExample / EnqueueExampleTask forwarders",
		`// ListExamples 实现 oapi.ServerInterface。oapi 生成的 params 这里忽略——
// 底层 handler 会通过 ShouldBindQuery 重新绑定，让校验链路统一。
func (s *APIServer) ListExamples(c *gin.Context, _ oapi.ListExamplesParams) {
	s.Example.List(c)
}

// CreateExample 实现 oapi.ServerInterface。
func (s *APIServer) CreateExample(c *gin.Context) {
	s.Example.Create(c)
}

// EnqueueExampleTask 实现 oapi.ServerInterface。
func (s *APIServer) EnqueueExampleTask(c *gin.Context) {
	s.Example.EnqueueTask(c)
}

`,
		"",
	)
}

// ---------------------------------------------------------------------------
// internal/repository/tx.go — dbFromContext / txFromContext 在删完 Example
// repository 后暂时无 caller，linter 会报 unused。它们是给 new-endpoint
// 生成的 repository.New<Name>Repository 用的，留着等真业务接入。在文件
// 末尾加一个静音哨兵 `var _ = dbFromContext`——比 //nolint:unused 注释
// 跨多个 linter 都通用。

func patchTxHelpers() error {
	const path = "internal/repository/tx.go"
	content, err := readFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(content, "_ = dbFromContext") {
		log.Printf("  · skip %s (tx helpers already silenced)", path)
		return nil
	}
	const sentinel = "\n// dbFromContext / txFromContext 是 repository 工具，给 new-endpoint\n" +
		"// 生成的真业务 repository 用。drop-example 跑过后暂时无 caller，下面的\n" +
		"// 静音哨兵让 linter 闭嘴。接入第一个真 repository 后删除本块。\n" +
		"var (\n" +
		"\t_ = dbFromContext\n" +
		"\t_ = txFromContext\n" +
		")\n"
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := writeFile(path, content+sentinel); err != nil {
		return err
	}
	log.Printf("  ✓ patched %s (silence dbFromContext/txFromContext linter)", path)
	return nil
}

// ---------------------------------------------------------------------------
// 跨包测试微调

func patchCrossPackageTests() error {
	// server_test.go: 删 /api/v1/examples NotFound 用例
	{
		const path = "internal/server_test.go"
		content, err := readFile(path)
		if err != nil {
			return err
		}
		re := regexp.MustCompile(`\t*\{path: "/api/v1/examples", wantCode: http\.StatusNotFound\},\n`)
		if re.MatchString(content) {
			if err := writeFile(path, re.ReplaceAllString(content, "")); err != nil {
				return err
			}
			log.Printf("  ✓ patched %s (drop /examples NotFound case)", path)
		}
	}

	// router_test.go: 删 Example fixture
	{
		const path = "internal/router/router_test.go"
		content, err := readFile(path)
		if err != nil {
			return err
		}
		re := regexp.MustCompile(`\t*Example:\s*&handler\.ExampleHandler\{\},\n`)
		if re.MatchString(content) {
			if err := writeFile(path, re.ReplaceAllString(content, "")); err != nil {
				return err
			}
			log.Printf("  ✓ patched %s (drop Example fixture)", path)
		}
	}

	// openapi_test.go: /api/v1/examples 换成 /api/v1/auth/me
	{
		const path = "internal/handler/openapi_test.go"
		content, err := readFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(content, `"/api/v1/examples"`) {
			content = strings.ReplaceAll(content, `"/api/v1/examples"`, `"/api/v1/auth/me"`)
			if err := writeFile(path, content); err != nil {
				return err
			}
			log.Printf("  ✓ patched %s (/examples → /auth/me)", path)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// api/openapi.yaml — 按文本块清理

func patchOpenAPIYAML() error {
	const path = "api/openapi.yaml"
	content, err := readFile(path)
	if err != nil {
		return err
	}

	// 4.1 paths: 删 /api/v1/examples 和 /api/v1/examples/tasks 两块
	for _, key := range []string{"/api/v1/examples/tasks", "/api/v1/examples"} {
		content = dropYAMLPathBlock(content, key)
	}

	// 4.2 components.schemas: 8 个 Example* schema
	schemas := []string{
		"Example",
		"CreateExampleReq",
		"ExampleEnvelope",
		"ListExamplesRes",
		"ListExamplesEnvelope",
		"EnqueueExampleTaskReq",
		"EnqueueExampleTaskRes",
		"EnqueueExampleTaskEnvelope",
	}
	for _, name := range schemas {
		content = dropYAMLSchemaBlock(content, name)
	}

	// 4.3 分组注释 + tags.example 项
	content = regexp.MustCompile(`(?m)^    # ---------- Example ----------\n`).ReplaceAllString(content, "")
	content = regexp.MustCompile(`(?m)^  - name: example\n(?:    .*\n)*`).ReplaceAllString(content, "")

	// 4.4 折叠多余空行
	content = regexp.MustCompile(`\n{3,}`).ReplaceAllString(content, "\n\n")

	if err := writeFile(path, content); err != nil {
		return err
	}
	log.Printf("  ✓ patched %s (paths/schemas/tags cleaned)", path)
	return nil
}

// dropYAMLPathBlock 从 paths 段里删 `  <key>:` 整块。Go 的 RE2 不支持
// lookahead，所以走"按行扫描 + 缩进判终止"的手工实现：
//   - 起：缩进=2、内容是 `<key>:`
//   - 终：再次出现缩进 ≤ 2 的非空非注释行（下一个同级 path 或上一级 section）
//   - 空行 / 4 空格 / 6 空格 起的行都算 body 内
func dropYAMLPathBlock(content, key string) string {
	return dropYAMLBlock(content, 2, key+":")
}

// dropYAMLSchemaBlock 删 `    <Name>:` schema 整块（起 4 空格缩进）。
func dropYAMLSchemaBlock(content, name string) string {
	return dropYAMLBlock(content, 4, name+":")
}

// dropYAMLBlock 删一个 YAML 块：起始行恰好是 indent 个空格加 head；body
// 是所有缩进 > indent 的行 + 空行；终止于第一个缩进 ≤ indent 的非空行。
func dropYAMLBlock(content string, indent int, head string) string {
	indentStr := strings.Repeat(" ", indent)
	startLine := indentStr + head
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		if lines[i] != startLine {
			out = append(out, lines[i])
			continue
		}
		// 命中起始行：跳过这行；继续吃 body 行；遇到同/更低缩进非空行停。
		i++
		for i < len(lines) {
			line := lines[i]
			if line == "" {
				i++
				continue
			}
			// 量缩进
			depth := 0
			for depth < len(line) && line[depth] == ' ' {
				depth++
			}
			// 跳过的行：缩进严格大于 indent
			if depth > indent {
				i++
				continue
			}
			// 缩进 ≤ indent 的行属于下一块，回退一位让主循环重新看它
			break
		}
		i--
	}
	return strings.Join(out, "\n")
}

// ---------------------------------------------------------------------------
// 校验 / 收尾

func validateGoSyntax(paths []string) error {
	fset := token.NewFileSet()
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if _, err := parser.ParseFile(fset, p, nil, parser.AllErrors); err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
	}
	return nil
}

func gofmtAll() error {
	// 给 patched 文件统一跑 gofmt，避免去字段后留下错位缩进。
	files := []string{
		"internal/server.go",
		"internal/router/router.go",
		"internal/worker.go",
		"internal/worker/handler.go",
		"internal/handler/openapi.go",
	}
	args := append([]string{"-w"}, files...)
	cmd := exec.Command("gofmt", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func goImportsAll() error {
	if _, err := exec.LookPath("goimports"); err != nil {
		// goimports 是可选的——make verify 里 lint 会再抓未用 import，这里
		// 缺失不致命。
		log.Printf("  · goimports not found in PATH, skipping (make verify 阶段 lint 仍会兜底)")
		return nil
	}
	files := []string{
		"internal/server.go",
		"internal/router/router.go",
		"internal/worker.go",
		"internal/handler/openapi.go",
	}
	args := append([]string{"-w"}, files...)
	cmd := exec.Command("goimports", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runGo 跑 `go <args...>`，stdout 直通，stderr 缓存并在出错时回显。
func runGo(args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprint(os.Stderr, stderr.String())
		return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	if stderr.Len() > 0 {
		fmt.Fprint(os.Stderr, stderr.String())
	}
	return nil
}

func runMake(target string) error {
	var stderr bytes.Buffer
	cmd := exec.Command("make", "--no-print-directory", target)
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// 把 stderr 转写到外面让用户看到失败原因；同时返回错误供 main fail-fast。
		fmt.Fprintln(os.Stderr, stderr.String())
		return fmt.Errorf("make %s: %w", target, err)
	}
	if stderr.Len() > 0 {
		// stderr 有内容但 cmd 成功——把它原样回显，避免吞掉 warn。
		fmt.Fprint(os.Stderr, stderr.String())
	}
	return nil
}

// 当前未使用，但便于将来扩展（例如 patch 完 dump 一份预 diff）。
var _ = filepath.Join
