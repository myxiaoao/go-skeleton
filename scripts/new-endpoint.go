//go:build ignore

// new-endpoint 给新模块从 api/openapi.yaml 反向生成分层骨架 +
// APIServer 转发方法 + router 注册。
//
// 入口：
//
//	go run scripts/new-endpoint.go <Name>
//	make new-endpoint NAME=<Name>       # 推荐
//
// 示例：
//
//	go run scripts/new-endpoint.go Order
//
// 工作流（与旧"复刻 Example 模板"版相反，yaml 是真相源）：
//  1. 读 api/openapi.yaml：找所有 operationId **包含 <Name>**（大小写不敏感）
//     的 operation。没找到一个就 fail-fast，提示先去 yaml 加路径。
//  2. 读 internal/oapi/oapi.gen.go：校验每个 operation 在 ServerInterface
//     上都有对应方法签名（即 yaml 已经 make oapi 过）。不一致也 fail-fast。
//  3. 每个 operation 推出"动作名"：默认 operationId 去掉 Name 后剩下的；
//     可被 yaml 里 `x-handler-method: <Action>` extension 覆盖。
//  4. 按动作集合渲染 handler / service / repository / model / task 五层：
//     handler 方法体按动作模板选（List/Create/Get/Update/Delete/EnqueueTask
//     + 兜底通用模板）；service / repository 方法返 NotImplementedYet 占位
//     ——业务实现填上后换具体错误码或 nil。
//  5. 注入 internal/server.go（按 // NEH ... 锚点）、internal/router/router.go
//     （按 yaml path 推 verb + path 注册）、internal/handler/openapi.go
//     （按 ServerInterface 方法签名注入 APIServer 转发方法）。
//
// 关键约定：
//   - operationId 含 <Name> = 该 operation 属于 <Name> 资源。复合资源歧义
//     时（例 NAME=Order 误命中 OrderPayment）让用户在 NAME 上更精确。
//   - yaml 改了必须先跑 make oapi 才能跑本脚本——脚本不替你跑，避免双向
//     生成耦合。
//   - 不改 api/openapi.yaml；不改 internal/oapi/oapi.gen.go；不自动跑
//     go build——靠 make verify 在最后 hook 编译期保险线。
//
// 不属于任何包，//go:build ignore 让 go build/test 跳过它。
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

var (
	layers     = []string{"handler", "service", "repository", "model", "task"}
	testLayers = []string{"handler", "service", "repository"} // model/task 是数据结构层，不生成测试

	camelCaseRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
)

// operation 是 yaml 里属于 <Name> 资源的一条 operation 的归一化表示。
// 一条 operation 同时承载：handler 方法名（生成代码用）、HTTP verb + path
// （router 注册用）、ServerInterface 方法名（APIServer 转发方法用）。
type operation struct {
	OperationID    string // yaml 原值，如 "createExample"
	HandlerMethod  string // 推出的 handler 方法名，如 "Create"
	HTTPVerb       string // "GET" / "POST" / ...
	Path           string // yaml 原 path，如 "/api/v1/examples/{id}"
	GinPath        string // gin 形式，如 "/:id"（去掉资源前缀后剩下的）
	IfaceMethod    string // oapi.ServerInterface 上的方法名，如 "CreateExample"
	IfaceSig       serverIfaceSig
	PathParamNames []string // path 里 {var} 按出现顺序的名字；空切片表示无 path 参数
	RequiresAuth   bool     // yaml security 含 bearerAuth：路由注册时塞 deps.AuthRequired
}

func main() {
	// 解析 args：第一个非 flag 是 NAME；--dry-run / -n 切到只 plan 不写盘。
	// 也接受环境变量 DRY_RUN=1（make new-endpoint NAME=Order DRY_RUN=1）。
	var name string
	dryRun := os.Getenv("DRY_RUN") == "1"
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--dry-run", "-n":
			dryRun = true
		case "":
			// skip
		default:
			if name == "" {
				name = arg
			}
		}
	}
	if name == "" {
		fatal(fmt.Errorf(`usage: new-endpoint <Name> [--dry-run]
  Name 必须 CamelCase 首字母大写，不含空格/特殊字符（如 Order、UserGroup）
  --dry-run / DRY_RUN=1：只打印将生成 / 注入的内容，不写盘`))
	}
	if !camelCaseRe.MatchString(name) {
		fatal(fmt.Errorf("NAME=%q 必须 CamelCase 首字母大写，仅字母数字（如 Order、UserGroup）", name))
	}
	lower := strings.ToLower(name)

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		fatal(fmt.Errorf("chdir: %w", err))
	}

	// ---- 1. 解析 yaml + oapi.gen.go ----
	ops, groupPath, err := collectOperations(name)
	if err != nil {
		fatal(err)
	}
	if len(ops) == 0 {
		fatal(fmt.Errorf(`api/openapi.yaml 里找不到 operationId 含 %q 的 operation。
先去 yaml 加路径（如 /api/v1/%ss）+ operationId（如 list%ss / create%s），跑 make oapi，再回来跑本脚本。`,
			name, lower, name, name))
	}

	if err := checkServerInterface(ops); err != nil {
		fatal(fmt.Errorf(`%w
提示：跑 make oapi 把 yaml 同步到 internal/oapi/oapi.gen.go 再来`, err))
	}

	// ---- 2. 预检查：文件不存在 + 锚点都在 ----
	if err := preflight(lower); err != nil {
		fatal(err)
	}

	// dry-run：跑到这里所有解析 / 校验都过了，打印 plan 不写盘。Plan 包括：
	//   - ops 表（method / path / handler / auth）
	//   - 将生成 / 修改的文件清单
	//   - group path
	// 不调任何 writeFile / patch*，直接 return。
	if dryRun {
		fmt.Print(renderDryRunPlan(name, lower, ops, groupPath))
		return
	}

	// ---- 3. 生成 5 个分层文件 ----
	files := map[string]string{
		fmt.Sprintf("internal/handler/%s.go", lower):    renderHandler(name, lower, ops),
		fmt.Sprintf("internal/service/%s.go", lower):    renderService(name, lower, ops),
		fmt.Sprintf("internal/repository/%s.go", lower): renderRepository(name, lower, ops),
		fmt.Sprintf("internal/model/%s.go", lower):      renderModel(name, lower),
		fmt.Sprintf("internal/task/%s.go", lower):       renderTask(name, lower),
	}
	for path, content := range files {
		if err := writeFile(path, content); err != nil {
			fatal(err)
		}
		fmt.Printf("✓ created %s\n", path)
	}

	// ---- 4. 生成测试模板 ----
	tests := map[string]string{
		fmt.Sprintf("internal/handler/%s_test.go", lower):    renderHandlerTest(name, lower, ops),
		fmt.Sprintf("internal/service/%s_test.go", lower):    renderServiceTest(name, lower, ops),
		fmt.Sprintf("internal/repository/%s_test.go", lower): renderRepositoryTest(name, lower, ops),
	}
	for path, content := range tests {
		if err := writeFile(path, content); err != nil {
			fatal(err)
		}
		fmt.Printf("✓ created %s\n", path)
	}

	// ---- 5. 注入 server.go ----
	if err := patchServer(name, lower); err != nil {
		fatal(err)
	}
	fmt.Println("✓ patched internal/server.go")

	// ---- 6. 注入 router.go ----
	if err := patchRouter(name, lower, ops, groupPath); err != nil {
		fatal(err)
	}
	fmt.Println("✓ patched internal/router/router.go")

	// ---- 7. 注入 internal/handler/openapi.go ----
	if err := patchAPIServer(name, ops); err != nil {
		fatal(err)
	}
	fmt.Println("✓ patched internal/handler/openapi.go")

	// ---- 8. gofmt + 锚点回检 + 语法回检 ----
	patched := []string{
		"internal/server.go",
		"internal/router/router.go",
		"internal/handler/openapi.go",
	}
	// router_test.go 是可选锚点宿主；存在就一起 gofmt + parseCheck。
	if _, err := os.Stat("internal/router/router_test.go"); err == nil {
		patched = append(patched, "internal/router/router_test.go")
	}
	if err := runGofmt(patched...); err != nil {
		fatal(fmt.Errorf("gofmt: %w", err))
	}
	allMarkers := serverMarkers()
	allMarkers = append(allMarkers, routerMarkers()...)
	allMarkers = append(allMarkers, apiServerMarkers()...)
	for _, m := range allMarkers {
		if err := requireMarker(m.file, m.name); err != nil {
			fatal(fmt.Errorf("注入后锚点丢失（脚本逻辑 bug）：%w", err))
		}
	}
	for _, f := range patched {
		if err := parseCheck(f); err != nil {
			fatal(err)
		}
	}

	// ---- 9. 打印剩余手工步骤 ----
	fmt.Print(renderNextSteps(name, lower, ops, groupPath))
}

// ---------------------------------------------------------------------------
// yaml 解析 + ServerInterface 校验

// collectOperations 解析 api/openapi.yaml，找所有 operationId 包含 name 的
// operation。资源名识别：operationId 大小写不敏感包含 name 即认为属于该资源。
//
// 返回：
//   - 排序后的 operation 列表
//   - groupPath: 给 router.RegisterRoutes 里的 r.Group 用的相对路径
//     （resourcePrefix 去掉 /api/v1 前缀；router.go 顶层 RegisterRoutes 已经
//     被挂在 /api/v1 group 下了）。例 /api/v1/orders → "/orders"；
//     /api/v1/order-items → "/order-items"。
func collectOperations(name string) ([]operation, string, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile("api/openapi.yaml")
	if err != nil {
		return nil, "", fmt.Errorf("load api/openapi.yaml: %w", err)
	}

	var ops []operation

	// PathItem.Operations() 把 GET/POST/PUT/DELETE/PATCH 等 verb 一并给出，
	// 顺序不保证——稳定排序到末尾。
	for path, item := range doc.Paths.Map() {
		// path 级 x-resource：所有 op 共享的默认归属。
		pathResource := xResourceFromExtensions(item.Extensions)
		for verb, op := range item.Operations() {
			if op.OperationID == "" {
				continue
			}
			if !belongsToResource(op, pathResource, name) {
				continue
			}

			handlerMethod, err := deriveHandlerMethod(op, name)
			if err != nil {
				return nil, "", fmt.Errorf("%s %s: %w", verb, path, err)
			}

			ifaceMethod := pascalize(op.OperationID)
			pathParams, err := extractPathParams(path)
			if err != nil {
				return nil, "", fmt.Errorf("%s %s: %w", verb, path, err)
			}
			ops = append(ops, operation{
				OperationID:    op.OperationID,
				HandlerMethod:  handlerMethod,
				HTTPVerb:       verb,
				Path:           path,
				IfaceMethod:    ifaceMethod,
				PathParamNames: pathParams,
				RequiresAuth:   requiresBearerAuth(op, doc),
			})
		}
	}

	// 按 (path, verb) 稳定排序：让生成的 router 注册 / APIServer 方法顺序可
	// 预测，避免 map 迭代非确定性导致 git diff 抖动。
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return verbRank(ops[i].HTTPVerb) < verbRank(ops[j].HTTPVerb)
	})

	// 计算每条 op 的 GinPath（去掉资源前缀后剩下的部分）。例：
	//   /api/v1/orders        → ""        （加到 r.Group("/orders") 下挂 GET ""）
	//   /api/v1/orders/{id}   → "/:id"
	//   /api/v1/orders/tasks  → "/tasks"
	resourcePrefix := findResourcePrefix(ops, name)
	for i := range ops {
		ops[i].GinPath = ginPath(ops[i].Path, resourcePrefix)
	}

	// router.RegisterRoutes 接到的 *gin.RouterGroup 已是 engine.Group("/api/v1")，
	// r.Group 的相对路径要把这层前缀去掉。yaml 习惯所有业务 path 都以
	// /api/v1 开头，命中即剥；不命中（罕见，如挂了 /admin 这种顶层路径）原样
	// 透传，由 caller 处理。
	groupPath := strings.TrimPrefix(resourcePrefix, "/api/v1")
	if groupPath == "" {
		// resourcePrefix 恰好等于 "/api/v1"——理论上 ops 至少一个 path 比这个长，
		// 不会发生；防御一下，让 r.Group("") 也能跑。
		groupPath = "/"
	}

	return ops, groupPath, nil
}

// verbRank 给 HTTP verb 排序权重——常见顺序 GET → POST → PUT → PATCH → DELETE，
// 让生成的路由注册看起来跟 REST 习惯一致。
func verbRank(verb string) int {
	switch strings.ToUpper(verb) {
	case "GET":
		return 0
	case "POST":
		return 1
	case "PUT":
		return 2
	case "PATCH":
		return 3
	case "DELETE":
		return 4
	default:
		return 5
	}
}

// deriveHandlerMethod 推出 handler 上对应的方法名。优先看 yaml extension
// `x-handler-method`（显式覆盖），否则按"operationId 去掉 Name 取剩余"推。
//
//	createOrder       → Create
//	listOrders        → List       （Orders 复数也认）
//	getOrder          → Get
//	enqueueOrderTask  → EnqueueTask
//	batchUpdateOrders → BatchUpdate
//
// 推不出（剩余为空 / 非字母）时返 error，让用户用 x-handler-method 显式指定。
func deriveHandlerMethod(op *openapi3.Operation, name string) (string, error) {
	if op.Extensions != nil {
		if raw, ok := op.Extensions["x-handler-method"]; ok {
			s, ok := raw.(string)
			if !ok {
				return "", fmt.Errorf("operationId=%q x-handler-method 必须是字符串", op.OperationID)
			}
			s = strings.TrimSpace(s)
			if !camelCaseRe.MatchString(s) {
				return "", fmt.Errorf("operationId=%q x-handler-method=%q 必须 CamelCase 首字母大写", op.OperationID, s)
			}
			return s, nil
		}
	}

	// 尝试两种去除形态：name + name+"s"（go-skeleton 命名习惯：listOrders/createOrder 并存）。
	id := op.OperationID
	for _, tryName := range []string{name + "s", name} {
		idx := strings.Index(strings.ToLower(id), strings.ToLower(tryName))
		if idx < 0 {
			continue
		}
		// 拼回 prefix + suffix。如果剩余为空（例 `order` operationId），
		// 这种约定不在本项目，让用户加 x-handler-method。
		remaining := id[:idx] + id[idx+len(tryName):]
		if remaining == "" {
			break
		}
		// 首字母大写。
		out := strings.ToUpper(remaining[:1]) + remaining[1:]
		if !camelCaseRe.MatchString(out) {
			continue
		}
		return out, nil
	}

	return "", fmt.Errorf("operationId=%q 推不出 handler 方法名（去掉 %q 后剩余为空或非法）；"+
		`yaml 里加 "x-handler-method: <Action>" 显式指定`, op.OperationID, name)
}

// belongsToResource 判定 op 是否属于 NAME 资源。三层语义（按优先级）：
//  1. operation 级 x-resource: 显式声明 → 严格按值匹配 NAME（大小写不敏感）
//  2. path 级 x-resource: 同 path 下所有 op 共享 → 同上严格匹配
//  3. fallback：operationId 大小写不敏感包含 NAME（向后兼容老 yaml）
//
// 第 1/2 层只要其中之一显式声明了 x-resource，**就走 x-resource 路径**（不会
// 再 fallback 到 operationId 包含）——避免开发者声明了 x-resource: OrderPayment
// 但 fallback 把它也归到 NAME=Order 下，违背显式声明的意图。
func belongsToResource(op *openapi3.Operation, pathResource, name string) bool {
	opResource := xResourceFromExtensions(op.Extensions)
	switch {
	case opResource != "":
		return strings.EqualFold(opResource, name)
	case pathResource != "":
		return strings.EqualFold(pathResource, name)
	default:
		return strings.Contains(strings.ToLower(op.OperationID), strings.ToLower(name))
	}
}

// xResourceFromExtensions 从 kin-openapi 的 Extensions map 里取 x-resource
// 的字符串值。值不是字符串视为未声明（返空串），不报错——让 caller fallback
// 到下一层语义。
func xResourceFromExtensions(ext map[string]any) string {
	if ext == nil {
		return ""
	}
	raw, ok := ext["x-resource"]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// extractPathParams 从 yaml path 字符串里按出现顺序提取 {var} 的 var 名。
//
//	/api/v1/orders               → []
//	/api/v1/orders/{id}          → ["id"]
//	/api/v1/orders/{order_id}    → ["order_id"]
//	/api/v1/users/{uid}/orders/{oid}  → ["uid", "oid"]
//
// path 参数 ≥2 时返 error——脚本模板只覆盖 0/1 个参数的常规 REST 形态；
// 多参数嵌套资源建议用 yaml extension `x-handler-method` 指定方法名后手写。
var pathParamRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func extractPathParams(path string) ([]string, error) {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	if len(names) > 1 {
		return nil, fmt.Errorf("path %q 含 %d 个参数 %v——脚本只支持 0/1 个 path 参数的常规模板；"+
			`多参数嵌套资源用 x-handler-method 指定方法名后手写 handler / 路由`,
			path, len(names), names)
	}
	return names, nil
}

// requiresBearerAuth 判定一个 operation 在路由注册时是否要塞 deps.AuthRequired。
// OpenAPI security 语义：
//
//	op.Security == nil           → 继承文档级 security
//	op.Security != nil && empty  → 显式关闭鉴权（OpenAPI 标准，空数组表示"覆盖全局，无鉴权"）
//	op.Security != nil && non-empty → 用 op-level 的
//
// 在选定生效列表后，扫每个 SecurityRequirement（一个 entry 是 map[string][]string）
// 看是否含 "bearerAuth" key（与项目 components.securitySchemes 里的命名对齐）。
// 命中一个就返 true——OR 语义。
//
// 其他 security scheme（API key / OAuth2 / ...）项目当前不支持，脚本忽略；
// 未来要支持时按 scheme 类型映射到不同中间件。
func requiresBearerAuth(op *openapi3.Operation, doc *openapi3.T) bool {
	// op.Security 是指针：nil → 继承全局；非 nil 但 len 0 → 显式关闭。
	// doc.Security 是值类型：nil 或空 list 都视作"无全局鉴权"。
	var reqs openapi3.SecurityRequirements
	switch {
	case op.Security != nil:
		reqs = *op.Security
	default:
		reqs = doc.Security
	}
	for _, req := range reqs {
		if _, ok := req["bearerAuth"]; ok {
			return true
		}
	}
	return false
}

// pascalize 把 "createExample" 转成 "CreateExample"——oapi-codegen 生成
// ServerInterface 上方法名的规则。
func pascalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// findResourcePrefix 找一组 operation path 的最长公共"资源前缀"——脚本据此
// 把 path 切成 group("/<resource>") + 子路径，便于生成 r.Group(...) 风格的
// router 注册。
//
// 启发式：所有 path 的最长公共前缀去掉末尾的 `/...{...}...` 子路径段。例
//
//	/api/v1/orders, /api/v1/orders/{id}, /api/v1/orders/tasks
//	→ 公共前缀 "/api/v1/orders"
//
// 单条 path 时直接取 path 本身（如果是 collection）或去掉末尾 {var} 段。
// 拿不准的 corner case 取整个 path 当 prefix——router 也能跑，只是 group 路径多点。
func findResourcePrefix(ops []operation, name string) string {
	if len(ops) == 0 {
		return ""
	}
	prefix := ops[0].Path
	for _, op := range ops[1:] {
		prefix = commonPrefix(prefix, op.Path)
	}
	// 截到末尾完整 segment：避免 "/api/v1/orders" 与 "/api/v1/order_items"
	// 公共前缀 "/api/v1/order" 这种情况（虽然命名习惯里 _ 少见）。
	if idx := strings.LastIndex(prefix, "/"); idx >= 0 && idx < len(prefix)-1 {
		// 末尾段如果是 {var}，回退一级让 group 路径更稳。
		tail := prefix[idx+1:]
		if strings.HasPrefix(tail, "{") {
			prefix = prefix[:idx]
		}
	}
	// 去末尾斜杠。
	prefix = strings.TrimRight(prefix, "/")
	return prefix
}

func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// ginPath 把 OpenAPI path 转成 gin 路由形态，并去掉 resourcePrefix。
//
//	/api/v1/orders         + prefix "/api/v1/orders" → ""
//	/api/v1/orders/{id}    + prefix "/api/v1/orders" → "/:id"
//	/api/v1/orders/tasks   + prefix "/api/v1/orders" → "/tasks"
func ginPath(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return ""
	}
	// {var} → :var
	re := regexp.MustCompile(`\{([^}]+)\}`)
	return re.ReplaceAllString(rest, ":$1")
}

// checkServerInterface 校验 internal/oapi/oapi.gen.go 的 ServerInterface 上
// 是否对每个 ops[i] 都有对应方法（且记录方法是否带第二参数 Params struct）。
func checkServerInterface(ops []operation) error {
	const path = "internal/oapi/oapi.gen.go"
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("缺 %s：先跑 make oapi", path)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	// 找 ServerInterface 的方法集。
	methods := serverInterfaceMethods(file)
	if methods == nil {
		return fmt.Errorf("%s 里没找到 ServerInterface（oapi 生成结构异常）", path)
	}

	missing := []string{}
	for i, op := range ops {
		sig, ok := methods[op.IfaceMethod]
		if !ok {
			missing = append(missing, op.IfaceMethod)
			continue
		}
		ops[i].IfaceSig = sig
	}
	if len(missing) > 0 {
		return fmt.Errorf("ServerInterface 缺方法：%s（yaml 改完忘了 make oapi？）",
			strings.Join(missing, ", "))
	}
	return nil
}

// serverIfaceSig 是 ServerInterface 上一个方法签名的归一化表示。oapi-codegen
// 生成三种形态：
//
//	GetHealth(c *gin.Context)                                — 单参（无 path / query 参数）
//	ListExamples(c *gin.Context, params ListExamplesParams)  — 带 Params struct（含 query）
//	GetDemo(c *gin.Context, id string)                        — path 参数走裸类型
//
// 仅 Params 形态在 APIServer 转发方法签名上要写第二参数 `oapi.XxxParams`；
// 裸 path 参数也要写第二参数（按裸类型 + 名字），所以 PathParam 也得记下来。
type serverIfaceSig struct {
	HasParams   bool   // 第二参数类型以 "Params" 结尾（如 ListExamplesParams）
	PathParam   string // path 参数名，非空说明形如 GetDemo(c, id string)
	PathParType string // path 参数类型（多数是 string）
}

func serverInterfaceMethods(file *ast.File) map[string]serverIfaceSig {
	out := map[string]serverIfaceSig{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "ServerInterface" {
				continue
			}
			iface, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				return nil
			}
			for _, m := range iface.Methods.List {
				ft, ok := m.Type.(*ast.FuncType)
				if !ok || len(m.Names) == 0 || ft.Params == nil {
					continue
				}
				sig := serverIfaceSig{}
				// Params.List 是按"同类型连续 names"分组的——一个 `c *gin.Context`
				// 占一个 entry，`id string` 占另一个。这里我们关注的就是第二个 entry。
				if len(ft.Params.List) >= 2 {
					second := ft.Params.List[1]
					typeStr := exprTypeName(second.Type)
					switch {
					case strings.HasSuffix(typeStr, "Params"):
						sig.HasParams = true
					default:
						// 形如 GetDemo(c, id string)：path 参数。
						if len(second.Names) > 0 {
							sig.PathParam = second.Names[0].Name
							sig.PathParType = typeStr
						}
					}
				}
				out[m.Names[0].Name] = sig
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// exprTypeName 把 ast 类型表达式转成最 readable 的字符串。本脚本只需识别：
//
//	*ast.Ident         { Name: "ListExamplesParams" } / { Name: "string" }
//	*ast.SelectorExpr  跨包 type 引用（oapi.gen.go 内部一般不出现，留分支防御）
func exprTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprTypeName(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + exprTypeName(t.X)
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// 预检查

// preflight 检查目标分层文件未存在、所有锚点都已就位。
func preflight(lower string) error {
	for _, layer := range layers {
		dst := fmt.Sprintf("internal/%s/%s.go", layer, lower)
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("已存在：%s（拒绝覆盖；先 rm 或换 NAME）", dst)
		}
	}
	for _, layer := range testLayers {
		dst := fmt.Sprintf("internal/%s/%s_test.go", layer, lower)
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("测试已存在：%s（拒绝覆盖）", dst)
		}
	}
	allMarkers := serverMarkers()
	allMarkers = append(allMarkers, routerMarkers()...)
	allMarkers = append(allMarkers, apiServerMarkers()...)
	for _, m := range allMarkers {
		if err := requireMarker(m.file, m.name); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 分层 render（按动作集合生成）

// renderHandler 生成 internal/handler/<lower>.go。
// handler 体按动作模板选择：List/Create/Get/Update/Delete/EnqueueTask + 兜底通用。
// service 的请求体结构（Create<Name>Req 等）让 service 包负责定义。
func renderHandler(name, lower string, ops []operation) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, `package handler

// %[1]sHandler 由 make new-endpoint NAME=%[1]s 按 api/openapi.yaml 反向生成。
// handler 层只做三件事：参数绑定 / 校验 + 调 service + 包响应。
// 业务规则写在 service 层；改字段先改 api/openapi.yaml 然后跑 make oapi。

import (
	"github.com/gin-gonic/gin"

	"go-skeleton/internal/service"
	"go-skeleton/pkg/response"
)

// %[1]sHandler 处理 %[2]s 资源的 HTTP 请求。
type %[1]sHandler struct {
	svc *service.%[1]sService
}

// New%[1]sHandler 构造 %[1]sHandler。
func New%[1]sHandler(svc *service.%[1]sService) *%[1]sHandler {
	return &%[1]sHandler{svc: svc}
}
`, name, lower)

	for _, op := range ops {
		fmt.Fprintln(&b)
		fmt.Fprint(&b, renderHandlerMethod(name, lower, op))
	}
	return b.String()
}

// renderHandlerMethod 按动作类型给一个方法挑模板。NotImplementedYet 占位
// 让骨架直接编译通过；业务实现填上后换 nil 或具体错误码。
func renderHandlerMethod(name, lower string, op operation) string {
	// pathVar / pathArg：op.PathParamNames[0] 决定从 c.Param 取啥、传 service 啥。
	// 没 path 参数时 pathArg 空，handler body 也不再做 c.Param。
	pathVar := ""
	pathArg := ""
	if len(op.PathParamNames) == 1 {
		pathVar = op.PathParamNames[0]
		pathArg = ", " + pathVar
	}
	switch op.HandlerMethod {
	case "List":
		return fmt.Sprintf(`// %[3]s 处理 %[5]s %[4]s。
func (h *%[1]sHandler) %[3]s(c *gin.Context) {
	res, err := h.svc.%[3]s(c.Request.Context())
	if err != nil {
		response.WriteError(c, err)
		return
	}
	response.WriteSuccess(c, res)
}
`, name, lower, op.HandlerMethod, op.Path, op.HTTPVerb)
	case "Create":
		return fmt.Sprintf(`// %[3]s 处理 %[5]s %[4]s。
func (h *%[1]sHandler) %[3]s(c *gin.Context) {
	res, err := h.svc.%[3]s(c.Request.Context())
	if err != nil {
		response.WriteError(c, err)
		return
	}
	response.WriteSuccess(c, res)
}
`, name, lower, op.HandlerMethod, op.Path, op.HTTPVerb)
	case "Get", "Delete":
		return fmt.Sprintf(`// %[3]s 处理 %[5]s %[4]s。
func (h *%[1]sHandler) %[3]s(c *gin.Context) {
	%[7]s := c.Param(%[8]q)
	res, err := h.svc.%[3]s(c.Request.Context()%[6]s)
	if err != nil {
		response.WriteError(c, err)
		return
	}
	response.WriteSuccess(c, res)
}
`, name, lower, op.HandlerMethod, op.Path, op.HTTPVerb, pathArg, pathVar, pathVar)
	case "Update":
		return fmt.Sprintf(`// %[3]s 处理 %[5]s %[4]s。
func (h *%[1]sHandler) %[3]s(c *gin.Context) {
	%[7]s := c.Param(%[8]q)
	res, err := h.svc.%[3]s(c.Request.Context()%[6]s)
	if err != nil {
		response.WriteError(c, err)
		return
	}
	response.WriteSuccess(c, res)
}
`, name, lower, op.HandlerMethod, op.Path, op.HTTPVerb, pathArg, pathVar, pathVar)
	case "EnqueueTask":
		return fmt.Sprintf(`// %[3]s 处理 %[5]s %[4]s——把任务投到 Asynq 队列。
func (h *%[1]sHandler) %[3]s(c *gin.Context) {
	res, err := h.svc.%[3]s(c.Request.Context())
	if err != nil {
		response.WriteError(c, err)
		return
	}
	response.WriteSuccess(c, res)
}
`, name, lower, op.HandlerMethod, op.Path, op.HTTPVerb)
	default:
		// 通用兜底：保留 TODO 让人补绑定 / 调 service。
		return fmt.Sprintf(`// %[3]s 处理 %[5]s %[4]s。
// TODO: 按业务字段补 ShouldBind / 调 service / 响应。
func (h *%[1]sHandler) %[3]s(c *gin.Context) {
	res, err := h.svc.%[3]s(c.Request.Context())
	if err != nil {
		response.WriteError(c, err)
		return
	}
	response.WriteSuccess(c, res)
}
`, name, lower, op.HandlerMethod, op.Path, op.HTTPVerb)
	}
}

// renderService 生成 internal/service/<lower>.go。
// service 方法签名按 handler 调用方式定（List 不带 id、Get/Update/Delete 带 id），
// body 一律返 errcode.NotImplementedYet 让骨架编译过 + 跑起来给"未实现"信号。
func renderService(name, lower string, ops []operation) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, `package service

// %[1]sService 由 make new-endpoint NAME=%[1]s 按 api/openapi.yaml 反向生成。
// 业务规则填这里。所有方法初始返 errcode.NotImplementedYet——把它换成 nil
// 或具体错误码就是落地业务的标志。

import (
	"context"

	"go-skeleton/pkg/errcode"
)

// %[1]sRepository 是 %[1]sService 对持久化层的最小依赖契约。手写于本包，
// 便于在 *_test.go 里 inline mock。
type %[1]sRepository interface {
	// TODO: 按真实业务定义查询方法签名。
}

// %[1]sQueue 是 %[1]sService 对 Asynq 队列的最小依赖契约（如果有异步任务）。
type %[1]sQueue interface {
	Available() bool
	// TODO: 按真实任务定义投递方法签名。
}

// %[1]sService 编排 %[2]s 资源的业务规则。
type %[1]sService struct {
	repo  %[1]sRepository
	queue %[1]sQueue
}

// New%[1]sService 构造 %[1]sService。queue 允许为 nil（无异步任务场景）。
func New%[1]sService(repo %[1]sRepository, queue %[1]sQueue) *%[1]sService {
	return &%[1]sService{repo: repo, queue: queue}
}
`, name, lower)

	for _, op := range ops {
		fmt.Fprintln(&b)
		fmt.Fprint(&b, renderServiceMethod(name, op))
	}
	return b.String()
}

// serviceParamName 返回 op 用在 service 方法签名里的 path 参数名。
// 默认取 yaml path 的实际名（如 order_id）；没声明 path 参数（不该走到 Get/
// Update/Delete case，防御 fallback）返 "id"。
func serviceParamName(op operation) string {
	if len(op.PathParamNames) == 1 {
		return op.PathParamNames[0]
	}
	return "id"
}

// renderServiceMethod 生成 service 方法签名 + 占位实现。
// 入参：context.Context + (Get/Update/Delete) id string + (Create/Update) 后续手补 req。
// 返回：(any, error) ——精确返回类型让填业务的人换。
func renderServiceMethod(name string, op operation) string {
	switch op.HandlerMethod {
	case "List":
		return fmt.Sprintf(`// %[2]s TODO: 列表查询。返回类型 / 参数（limit/offset/filter）按业务补。
func (s *%[1]sService) %[2]s(ctx context.Context) (any, error) {
	_ = ctx
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod)
	case "Create":
		return fmt.Sprintf(`// %[2]s TODO: 创建。请求结构 / 返回 model 按业务补。
func (s *%[1]sService) %[2]s(ctx context.Context) (any, error) {
	_ = ctx
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod)
	case "Get":
		pathVar := serviceParamName(op)
		return fmt.Sprintf(`// %[2]s TODO: 按 %[3]s 查单条。
func (s *%[1]sService) %[2]s(ctx context.Context, %[3]s string) (any, error) {
	_ = ctx
	_ = %[3]s
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod, pathVar)
	case "Update":
		pathVar := serviceParamName(op)
		return fmt.Sprintf(`// %[2]s TODO: 按 %[3]s 更新。请求结构按业务补。
func (s *%[1]sService) %[2]s(ctx context.Context, %[3]s string) (any, error) {
	_ = ctx
	_ = %[3]s
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod, pathVar)
	case "Delete":
		pathVar := serviceParamName(op)
		return fmt.Sprintf(`// %[2]s TODO: 按 %[3]s 删除。
func (s *%[1]sService) %[2]s(ctx context.Context, %[3]s string) (any, error) {
	_ = ctx
	_ = %[3]s
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod, pathVar)
	case "EnqueueTask":
		return fmt.Sprintf(`// %[2]s TODO: 投异步任务。请求结构 / 任务 payload 按业务补。
func (s *%[1]sService) %[2]s(ctx context.Context) (any, error) {
	_ = ctx
	if s.queue == nil || !s.queue.Available() {
		return nil, errcode.QueueUnavailable
	}
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod)
	default:
		return fmt.Sprintf(`// %[2]s TODO: 按业务补参数 / 返回类型。
func (s *%[1]sService) %[2]s(ctx context.Context) (any, error) {
	_ = ctx
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod)
	}
}

// renderRepository 生成 internal/repository/<lower>.go。
// 与 service 的 Repository 接口对应——但接口字段还没定，这里只给 struct +
// New。新增方法由开发者按业务 SQL 自补，不强行预生成模板（OpenAPI 不知道
// DB schema，生成的 GORM 调用一定要返工）。
func renderRepository(name, lower string, _ []operation) string {
	return fmt.Sprintf(`package repository

// %[1]sRepository 由 make new-endpoint NAME=%[1]s 生成的骨架。
// 唯一允许写 GORM 或原生 SQL 的层。
//
// 加查询方法的步骤：
//  1. 在 internal/service/%[2]s.go 的 %[1]sRepository 接口里加方法签名
//  2. 在这里实现，使用 db.WithContext(ctx)；事务走 repository.InTx
//  3. 在 service / handler 测试里通过 mock%[1]sRepo 给 stub

import (
	"gorm.io/gorm"
)

type %[1]sRepository struct {
	db *gorm.DB
}

// New%[1]sRepository 构造 %[1]sRepository。
func New%[1]sRepository(db *gorm.DB) *%[1]sRepository {
	return &%[1]sRepository{db: db}
}
`, name, lower)
}

// renderModel 生成 internal/model/<lower>.go——纯 GORM struct 骨架。
func renderModel(name, lower string) string {
	return fmt.Sprintf(`package model

// %[1]s 是 %[2]s 资源的持久化结构。由 make new-endpoint NAME=%[1]s 生成。
// GORM AutoMigrate 已废弃——表结构走 migrations/*.sql。这里只声明 Go 侧
// 的字段映射，与迁移文件保持一致由开发者维护。

import "time"

// %[1]s TODO: 按业务字段补 columns。
type %[1]s struct {
	ID        uint      `+"`gorm:\"primaryKey\"`"+`
	CreatedAt time.Time
	UpdatedAt time.Time
}
`, name, lower)
}

// renderTask 生成 internal/task/<lower>.go——Asynq 任务类型骨架。
// 如果该资源没有 EnqueueTask 动作，这个文件不会被业务用到（保留是为了
// 让脚本生成的目录形态对齐——同时也方便未来加任务时已经有 file）。
func renderTask(name, lower string) string {
	return fmt.Sprintf(`package task

// %[1]s 资源相关的 Asynq 任务类型。由 make new-endpoint NAME=%[1]s 生成。
// 如果该资源没有异步任务，本文件可以删；保留是为了方便后续加任务时已经有 file。

const (
	// Type%[1]sExample TODO: 改成真实的任务类型常量，例 "%[2]s:created"。
	Type%[1]sExample = "%[2]s:example"
)
`, name, lower)
}

// ---------------------------------------------------------------------------
// 测试模板

func renderHandlerTest(name, lower string, _ []operation) string {
	return fmt.Sprintf(`package handler

// %[1]sHandler smoke 测试：由 make new-endpoint NAME=%[1]s 生成。
// 当前断言"路由能挂上"——业务实现填进 service 后按 example_test.go 风格
// 追加 binding / 错误码 / 边界值用例。

import (
	"testing"

	"go.uber.org/zap"

	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/validator"
)

func init() {
	applog.SetLogger(zap.NewNop())
	validator.InitValidator()
}

// Test%[1]sHandlerSmoke 占位测试，避免空测试文件被 lint 抱怨。
// 业务实现后删掉它，按 example_test.go 风格写真实用例。
func Test%[1]sHandlerSmoke(t *testing.T) {
	_ = t
}
`, name, lower)
}

func renderServiceTest(name, lower string, _ []operation) string {
	return fmt.Sprintf(`package service

// %[1]sService smoke 测试：由 make new-endpoint NAME=%[1]s 生成。
// 业务实现填上后按 example_test.go 风格补真实用例（errcode 断言走
// errors.As(err, &ec) + ec.Code() 比较）。

import (
	"testing"

	"go.uber.org/zap"

	applog "go-skeleton/pkg/log"
)

func init() {
	applog.SetLogger(zap.NewNop())
}

func Test%[1]sServiceSmoke(t *testing.T) {
	_ = t
}
`, name, lower)
}

func renderRepositoryTest(name, lower string, _ []operation) string {
	return fmt.Sprintf(`package repository

// %[1]sRepository smoke 测试：由 make new-endpoke NAME=%[1]s 生成。
// 真实查询出现后按 example_test.go 风格用 GORM DryRun 捕 SQL 断言。

import (
	"testing"

	"go.uber.org/zap"

	applog "go-skeleton/pkg/log"
)

func init() {
	applog.SetLogger(zap.NewNop())
}

func Test%[1]sRepositorySmoke(t *testing.T) {
	_ = t
}
`, name, lower)
}

// ---------------------------------------------------------------------------
// 注入：server.go / router.go / openapi.go

type marker struct {
	file string
	name string
}

func serverMarkers() []marker {
	return []marker{
		{"internal/server.go", "handlers-fields"},
		{"internal/server.go", "handlers-deps"},
		{"internal/server.go", "handlers-construct"},
		{"internal/server.go", "handlers-return"},
	}
}

func routerMarkers() []marker {
	return []marker{
		{"internal/router/router.go", "deps-fields"},
		{"internal/router/router.go", "routes-register"},
	}
}

// routerTestMarker 是 internal/router/router_test.go::buildEngine 里 deps
// fixture 的注入点。新增资源时往这里塞一个 zero-value handler，让 spec
// coverage 测试（TestRouterCoversAllSpecOperations）拿到的 Dependencies
// 覆盖到 yaml 新增路径，而不是 404。
//
// 注意：测试文件是可选锚点——如果 fixture 缺锚点（例如骨架仓库自己删过），
// 注入步骤被跳过而不是 fail-fast，让脚本仍可用。
func routerTestMarker() marker {
	return marker{"internal/router/router_test.go", "test-deps"}
}

func apiServerMarkers() []marker {
	return []marker{
		{"internal/handler/openapi.go", "apiserver-fields"},
		{"internal/handler/openapi.go", "apiserver-methods"},
	}
}

// patchServer 注入 internal/server.go：handlers struct 字段 + 装配链 + 返回字段。
func patchServer(name, lower string) error {
	patches := []struct {
		marker string
		lines  []string
	}{
		{"handlers-fields", []string{
			fmt.Sprintf("\t%s *handler.%sHandler", name, name),
		}},
		{"handlers-deps", []string{
			fmt.Sprintf("\t%sRepository := repository.New%sRepository(db)", lower, name),
			fmt.Sprintf("\t%sService := service.New%sService(%sRepository, reg.Queue)", lower, name, lower),
		}},
		{"handlers-construct", []string{
			fmt.Sprintf("\t%sH := handler.New%sHandler(%sService)", lower, name, lower),
		}},
		{"handlers-return", []string{
			fmt.Sprintf("\t\t%s: %sH,", name, lower),
		}},
	}
	for _, p := range patches {
		for _, line := range p.lines {
			if err := insertBeforeMarker("internal/server.go", p.marker, line); err != nil {
				return err
			}
		}
	}
	return nil
}

// patchRouter 注入 internal/router/router.go：
//   - Dependencies 字段
//   - registerRoutes 调用
//   - 末尾 append register<Name>Routes 函数（按 ops 推 verb + path）
func patchRouter(name, lower string, ops []operation, groupPath string) error {
	patches := []struct {
		marker string
		line   string
	}{
		{"deps-fields", fmt.Sprintf("\t%s *handler.%sHandler", name, name)},
		{"routes-register", fmt.Sprintf("\tregister%sRoutes(r, deps)", name)},
	}
	for _, p := range patches {
		if err := insertBeforeMarker("internal/router/router.go", p.marker, p.line); err != nil {
			return err
		}
	}
	if err := appendRouterFunc(name, ops, groupPath); err != nil {
		return err
	}
	return patchRouterTest(name)
}

// patchRouterTest 给 internal/router/router_test.go::buildEngine 的 deps
// fixture 注入新资源的 zero-value handler。锚点存在就注，没有就跳过——
// 测试文件可选，不强制存在。
func patchRouterTest(name string) error {
	m := routerTestMarker()
	if _, err := os.Stat(m.file); err != nil {
		return nil
	}
	if err := requireMarker(m.file, m.name); err != nil {
		// 没锚点视为开发者主动移除，跳过而非 fail-fast。
		return nil
	}
	line := fmt.Sprintf("\t\t%s: &handler.%sHandler{},", name, name)
	if err := insertBeforeMarker(m.file, m.name, line); err != nil {
		return err
	}
	return nil
}

// appendRouterFunc 把 register<Name>Routes 函数 append 到 router.go 末尾。
// groupPath 来自 collectOperations 算出的 resourcePrefix 去掉 /api/v1。
func appendRouterFunc(name string, ops []operation, groupPath string) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, `
// register%[1]sRoutes 挂 %[2]s 路由——由 make new-endpoint NAME=%[1]s 按
// api/openapi.yaml 反向生成。新增 / 删除路径走 yaml + make oapi +
// make new-endpoint，不要手改这里。
func register%[1]sRoutes(r *gin.RouterGroup, deps Dependencies) {
	if deps.%[1]s == nil {
		return
	}

	g := r.Group("%[2]s")
`, name, groupPath)
	// 拆成两组写：先无鉴权，再走 g.Use(deps.AuthRequired) 后的鉴权组——
	// 这样不需要给每条 RequiresAuth 路由单独 prepend middleware 参数，也避免
	// authRequired==nil 时 nil middleware 当成 handler 调用炸进 gin。
	// 任一组为空时整段省略；全部需要鉴权时所有路由都跑在第二组。
	var publicOps, authOps []operation
	for _, op := range ops {
		if op.RequiresAuth {
			authOps = append(authOps, op)
		} else {
			publicOps = append(publicOps, op)
		}
	}
	writeRouteGroup(&b, "g", name, publicOps)
	if len(authOps) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "\t// yaml security: bearerAuth → 这一组需要鉴权。deps.AuthRequired 未注入时跳过整组。")
		fmt.Fprintln(&b, "\tif deps.AuthRequired != nil {")
		fmt.Fprintln(&b, "\t\tauthed := g.Group(\"\", deps.AuthRequired)")
		writeRouteGroup(&b, "authed", name, authOps)
		fmt.Fprintln(&b, "\t}")
	}
	fmt.Fprintln(&b, "}")

	const path = "internal/router/router.go"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s for append: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		return fmt.Errorf("append to %s: %w", path, err)
	}
	return nil
}

// writeRouteGroup 把 ops 里每条路由按 verb 写到 b。group 是 gin RouterGroup
// 变量名（"g" 或鉴权子组里的 "authed"）；authed 组在 if 块里要多一层缩进。
func writeRouteGroup(b *bytes.Buffer, group, name string, ops []operation) {
	indent := "\t"
	if group != "g" {
		indent = "\t\t"
	}
	for _, op := range ops {
		method := strings.ToUpper(op.HTTPVerb)
		fmt.Fprintf(b, "%s%s.%s(%q, deps.%s.%s)\n",
			indent, group, method, op.GinPath, name, op.HandlerMethod)
	}
}

// patchAPIServer 注入 internal/handler/openapi.go：
//   - APIServer struct 加 <Name> 字段
//   - 文件末尾按 ServerInterface 方法签名加 N 个转发方法
func patchAPIServer(name string, ops []operation) error {
	const path = "internal/handler/openapi.go"

	// 字段注入：APIServer struct 末尾。
	if err := insertBeforeMarker(path, "apiserver-fields",
		fmt.Sprintf("\t%s *%sHandler", name, name)); err != nil {
		return err
	}

	// 方法注入：按 ops 依次插入，每个方法的签名要跟 ServerInterface 上的
	// 精确对齐——HasParams / PathParam 决定第二参数形态。转发体里 path
	// 参数原样转给底层 handler 方法（底层方法签名由 service 层定）。
	for _, op := range ops {
		var sig, body string
		switch {
		case op.IfaceSig.HasParams:
			sig = fmt.Sprintf("func (s *APIServer) %s(c *gin.Context, _ oapi.%sParams)",
				op.IfaceMethod, op.IfaceMethod)
			body = fmt.Sprintf("s.%s.%s(c)", name, op.HandlerMethod)
		case op.IfaceSig.PathParam != "":
			// 形如 GetDemo(c *gin.Context, id string)：APIServer 转发方法签名也
			// 必须带 `id <type>`；handler 方法体里自己再 c.Param("id") 取一遍——
			// 这个冗余有意为之，让所有 handler 方法签名统一只接 *gin.Context，
			// 避免下游 service 入参类型在生成路径里飘。
			sig = fmt.Sprintf("func (s *APIServer) %s(c *gin.Context, _ %s)",
				op.IfaceMethod, op.IfaceSig.PathParType)
			body = fmt.Sprintf("s.%s.%s(c)", name, op.HandlerMethod)
		default:
			sig = fmt.Sprintf("func (s *APIServer) %s(c *gin.Context)", op.IfaceMethod)
			body = fmt.Sprintf("s.%s.%s(c)", name, op.HandlerMethod)
		}
		method := fmt.Sprintf(`
// %[1]s 实现 oapi.ServerInterface（由 make new-endpoint 生成）。
%[2]s {
	%[3]s
}
`, op.IfaceMethod, sig, body)
		if err := insertBeforeMarker(path, "apiserver-methods", strings.TrimRight(method, "\n")); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 通用 helpers

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// requireMarker 校验 file 里有 `// NEH <marker>` 全行注释（允许行首空白）。
// 全行匹配避免 godoc 里提到锚点名时被误命中。
func requireMarker(file, marker string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	re := regexp.MustCompile(`(?m)^\s*// NEH ` + regexp.QuoteMeta(marker) + `$`)
	if !re.Match(b) {
		return fmt.Errorf("%s 缺锚点 '// NEH %s'，无法幂等注入", file, marker)
	}
	return nil
}

// insertBeforeMarker 在 file 里把 line 插到首个 `// NEH <marker>` 全行注释之前。
// 多次调用相同 marker 时新插入行依次堆在 marker 之前（marker 行未被消耗）。
func insertBeforeMarker(file, marker, line string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	lines := strings.Split(string(b), "\n")
	re := regexp.MustCompile(`^\s*// NEH ` + regexp.QuoteMeta(marker) + `$`)

	out := make([]string, 0, len(lines)+1)
	inserted := false
	for _, l := range lines {
		if !inserted && re.MatchString(l) {
			out = append(out, line)
			inserted = true
		}
		out = append(out, l)
	}
	if !inserted {
		return fmt.Errorf("%s: marker '// NEH %s' not matched (should have been caught by requireMarker)", file, marker)
	}
	return os.WriteFile(file, []byte(strings.Join(out, "\n")), 0o644)
}

func parseCheck(path string) error {
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, path, nil, parser.AllErrors); err != nil {
		return fmt.Errorf("注入后 parse %s 失败：%w", path, err)
	}
	return nil
}

func runGofmt(paths ...string) error {
	args := append([]string{"-w"}, paths...)
	cmd := exec.Command("gofmt", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "new-endpoint:", err)
	os.Exit(1)
}

// renderDryRunPlan 给 --dry-run 模式打印的执行计划。让用户在跑真正生成
// 之前 review：NAME 匹配到了哪些 op、router 路径推得对不对、auth 分组、
// 将创建 / 修改哪些文件。pure plan，不写盘。
func renderDryRunPlan(name, lower string, ops []operation, groupPath string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "\n[DRY-RUN] new-endpoint NAME=%s\n", name)
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "Matched %d operation(s) (r.Group(%q) under /api/v1):\n", len(ops), groupPath)
	maxPathLen := 0
	for _, op := range ops {
		if l := len(op.Path); l > maxPathLen {
			maxPathLen = l
		}
	}
	for _, op := range ops {
		group := "public"
		if op.RequiresAuth {
			group = "bearerAuth"
		}
		paramHint := ""
		if len(op.PathParamNames) == 1 {
			paramHint = "  c.Param(\"" + op.PathParamNames[0] + "\")"
		}
		fmt.Fprintf(&b, "  %-6s %-*s  %-10s → %sHandler.%s%s\n",
			strings.ToUpper(op.HTTPVerb), maxPathLen, op.Path, group, name, op.HandlerMethod, paramHint)
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Files to be created:")
	for _, layer := range layers {
		fmt.Fprintf(&b, "  + internal/%s/%s.go\n", layer, lower)
	}
	for _, layer := range testLayers {
		fmt.Fprintf(&b, "  + internal/%s/%s_test.go\n", layer, lower)
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Files to be modified (via // NEH ... anchors):")
	fmt.Fprintln(&b, "  ~ internal/server.go              (handlers struct + assembly)")
	fmt.Fprintln(&b, "  ~ internal/router/router.go       (Dependencies + register"+name+"Routes)")
	fmt.Fprintln(&b, "  ~ internal/handler/openapi.go     (APIServer field + ServerInterface forwarders)")
	if _, err := os.Stat("internal/router/router_test.go"); err == nil {
		fmt.Fprintln(&b, "  ~ internal/router/router_test.go  (buildEngine deps fixture)")
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Re-run without --dry-run to apply.")
	return b.String()
}

// renderNextSteps 跑完打印的提示——业务实现填进 service / repository 后
// 跑 make verify 确认全链路绿。
//
// 输出两段：
//  1. router 表：按 yaml 实际 verb + path 列出每条路由，并标 public / bearerAuth
//     分组——比"方法集"更接近 gin 真实注册行为，便于 review
//  2. 落地业务清单
func renderNextSteps(name, lower string, ops []operation, groupPath string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "\n✅ %s 骨架已生成 + 装配 + 注入完成。\n", name)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "router 表（按 api/openapi.yaml 反推；r.Group(\""+groupPath+"\") 下挂）:")
	// 算列宽：verb 最长 6（DELETE）；path 用每条 op.Path 算最长，再 +2 pad。
	maxPathLen := 0
	for _, op := range ops {
		if l := len(op.Path); l > maxPathLen {
			maxPathLen = l
		}
	}
	for _, op := range ops {
		group := "public"
		if op.RequiresAuth {
			group = "bearerAuth"
		}
		fmt.Fprintf(&b, "  %-6s %-*s  %-10s → %sHandler.%s\n",
			strings.ToUpper(op.HTTPVerb), maxPathLen, op.Path, group, name, op.HandlerMethod)
	}

	fmt.Fprintf(&b, `
现在仓库应当 make verify 通过——所有方法返 NotImplementedYet 占位。

落地业务的步骤：
  1. internal/service/%[2]s.go：补 %[1]sRepository / %[1]sQueue 接口签名 +
     方法入参/返回类型，去掉 errcode.NotImplementedYet
  2. internal/repository/%[2]s.go：实现 service 里加的接口方法（GORM/原生 SQL）
  3. internal/model/%[2]s.go：按真实表结构补字段
  4. internal/handler/%[2]s.go：补 c.ShouldBindJSON / Query 把 req 传给 service
  5. internal/task/%[2]s.go：如果有异步任务，补 payload struct + NewXxxTask 工厂
  6. make verify 通过即合 PR
`, name, lower)
	return b.String()
}
