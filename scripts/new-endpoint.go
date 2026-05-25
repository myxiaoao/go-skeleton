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
	OperationID     string // yaml 原值，如 "createExample"
	HandlerMethod   string // 推出的 handler 方法名，如 "Create"
	HTTPVerb        string // "GET" / "POST" / ...
	Path            string // yaml 原 path，如 "/api/v1/examples/{id}"
	GinPath         string // gin 形式，如 "/:id"（去掉资源前缀后剩下的）
	IfaceMethod     string // oapi.ServerInterface 上的方法名，如 "CreateExample"
	IfaceSig        serverIfaceSig
	IsPathParameter bool // path 有没有 {var}
	RequiresAuth    bool // yaml security 含 bearerAuth：路由注册时塞 deps.AuthRequired
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "" {
		fatal(fmt.Errorf(`usage: new-endpoint <Name>
  Name 必须 CamelCase 首字母大写，不含空格/特殊字符（如 Order、UserGroup）`))
	}
	name := os.Args[1]
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
	ops, err := collectOperations(name)
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
	if err := patchRouter(name, lower, ops); err != nil {
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
	fmt.Print(renderNextSteps(name, lower, ops))
}

// ---------------------------------------------------------------------------
// yaml 解析 + ServerInterface 校验

// collectOperations 解析 api/openapi.yaml，找所有 operationId 包含 name 的
// operation。资源名识别：operationId 大小写不敏感包含 name 即认为属于该资源。
func collectOperations(name string) ([]operation, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile("api/openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("load api/openapi.yaml: %w", err)
	}

	lowerName := strings.ToLower(name)
	var ops []operation

	// PathItem.Operations() 把 GET/POST/PUT/DELETE/PATCH 等 verb 一并给出，
	// 顺序不保证——稳定排序到末尾。
	for path, item := range doc.Paths.Map() {
		for verb, op := range item.Operations() {
			if op.OperationID == "" {
				continue
			}
			if !strings.Contains(strings.ToLower(op.OperationID), lowerName) {
				continue
			}

			handlerMethod, err := deriveHandlerMethod(op, name)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", verb, path, err)
			}

			ifaceMethod := pascalize(op.OperationID)
			ops = append(ops, operation{
				OperationID:     op.OperationID,
				HandlerMethod:   handlerMethod,
				HTTPVerb:        verb,
				Path:            path,
				IfaceMethod:     ifaceMethod,
				IsPathParameter: strings.Contains(path, "{"),
				RequiresAuth:    requiresBearerAuth(op, doc),
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

	return ops, nil
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
	pathArg := ""
	if op.IsPathParameter {
		pathArg = ", id"
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
	id := c.Param("id")
	res, err := h.svc.%[3]s(c.Request.Context()%[6]s)
	if err != nil {
		response.WriteError(c, err)
		return
	}
	response.WriteSuccess(c, res)
}
`, name, lower, op.HandlerMethod, op.Path, op.HTTPVerb, pathArg)
	case "Update":
		return fmt.Sprintf(`// %[3]s 处理 %[5]s %[4]s。
func (h *%[1]sHandler) %[3]s(c *gin.Context) {
	id := c.Param("id")
	res, err := h.svc.%[3]s(c.Request.Context()%[6]s)
	if err != nil {
		response.WriteError(c, err)
		return
	}
	response.WriteSuccess(c, res)
}
`, name, lower, op.HandlerMethod, op.Path, op.HTTPVerb, pathArg)
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
		return fmt.Sprintf(`// %[2]s TODO: 按 id 查单条。
func (s *%[1]sService) %[2]s(ctx context.Context, id string) (any, error) {
	_ = ctx
	_ = id
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod)
	case "Update":
		return fmt.Sprintf(`// %[2]s TODO: 按 id 更新。请求结构按业务补。
func (s *%[1]sService) %[2]s(ctx context.Context, id string) (any, error) {
	_ = ctx
	_ = id
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod)
	case "Delete":
		return fmt.Sprintf(`// %[2]s TODO: 按 id 删除。
func (s *%[1]sService) %[2]s(ctx context.Context, id string) (any, error) {
	_ = ctx
	_ = id
	return nil, errcode.NotImplementedYet
}
`, name, op.HandlerMethod)
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
func patchRouter(name, lower string, ops []operation) error {
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
	return appendRouterFunc(name, lower, ops)
}

// appendRouterFunc 把 register<Name>Routes 函数 append 到 router.go 末尾。
// 按 yaml path 推 r.Group("/<resource>") + 子路径注册。
func appendRouterFunc(name, lower string, ops []operation) error {
	groupPath := groupPathFor(name, lower)

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

// groupPathFor 给 r.Group 选一个路径。约定走 "/<lower(name)>s" 形态——
// 与现有 example 路由一致；如果 yaml 里资源前缀不是这个形态（罕见），
// 生成的 group path 可能与 path 拼起来不完全等于 yaml，但 gin 路由实际效果
// 不受影响（OpenAPI 的 verify 已经保证 ServerInterface 在 oapi 这一侧契约对齐）。
func groupPathFor(name, lower string) string {
	_ = name
	return "/" + lower + "s"
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

// renderNextSteps 跑完打印的提示——业务实现填进 service / repository 后
// 跑 make verify 确认全链路绿。
func renderNextSteps(name, lower string, ops []operation) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, `
✅ %[1]s 骨架已生成 + 装配 + 注入完成。生成的方法集（基于 api/openapi.yaml）：
`, name)
	for _, op := range ops {
		fmt.Fprintf(&b, "   %s %-25s → %sHandler.%s\n",
			strings.ToUpper(op.HTTPVerb), op.Path, name, op.HandlerMethod)
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
