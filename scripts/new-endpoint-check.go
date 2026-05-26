//go:build ignore

// new-endpoint-check 是 make new-endpoint 的只读"drift detector"——
// 重新解析 api/openapi.yaml，按 x-resource / fallback 把 operation 分组成资源，
// 然后逐一对 internal/{handler,service,router,handler/openapi.go,server.go,
// router/router_test.go} 做存在性 + 路径 + 鉴权分组的一致性检查。**不写盘**、
// 不删代码、不重生成。
//
// 调用：
//
//	make new-endpoint-check               # 扫所有资源
//	make new-endpoint-check NAME=Order    # 只扫 Order
//
// 输出按严重度分三档：
//
//	[!] Missing  — yaml 有该 operation / 资源，但代码端完全没生成
//	[~] Stale    — 代码端残留，yaml 已 rename / delete operation
//	[-] Mismatch — 都有但 path / verb / auth 分组对不上
//
// 有任何 finding → exit 1。**不并入 make verify**（避免 schema 更新让 PR 抖动），
// 单跑作为调试入口；CI 想接进定时扫的话另起 job。
//
// ⚠️ 共享解析逻辑提示：
// 下面的 belongsToResource / xResourceFromExtensions / extractPathParams /
// requiresBearerAuth / camelCaseRe / pathParamRe 等函数与 scripts/new-endpoint.go
// 是**同源 copy**——改一处务必改两处，否则两边对 yaml 的归属判断会漂移。
// 之所以没抽 internal/yamlspec 共享包，是因为 scripts/ 下每个脚本都是
// //go:build ignore + package main，"go run scripts/X.go" 单文件运行约定不
// 兼容子包 import；抽包要把所有 go run scripts/X.go 调用点改成 go run ./scripts/cmd/X
// 形式，工程面太大。共享逻辑只有 ~80 行、收敛在几个小函数里，copy 成本可控。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// builtinResources 是 oapi.ServerInterface 里"非业务"的预置资源，不走
// make new-endpoint 生成，因此 check 也跳过它们。新增 builtin 时同步加这。
var builtinResources = map[string]bool{
	"Auth":    true, // /auth/* (CreateAuthToken / GetAuthMe)
	"Health":  true, // /health (GetHealth) + /livez (GetLivez)
	"OpenAPI": true, // /openapi.json (GetOpenAPISpec)
}

// builtinOpToResource 把那些**资源名跟 operationId 不字面相关**的 builtin
// op 显式映射到资源——避免 fallback 推断把 `getLivez` 错归到 Unknown。其余
// 含资源名的 op（如 `createAuthToken` 含 "Auth"）走通用 fallback 即可。
// 新增 builtin op 时同步加这。
var builtinOpToResource = map[string]string{
	"getLivez": "Health",
}

var camelCaseRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// finding 是单条 drift 告警。severity 决定输出前缀（! / ~ / -），message 是
// 人读的描述。同一资源下的 findings 按 severity 再按 message 排序输出。
type finding struct {
	resource string
	severity string // "missing" | "stale" | "mismatch"
	message  string
}

// operation 与 scripts/new-endpoint.go::operation 结构对齐（仅保留 check
// 用到的字段），便于读懂代码时直接照原文映射。
type operation struct {
	OperationID    string
	HandlerMethod  string
	HTTPVerb       string
	Path           string
	IfaceMethod    string
	PathParamNames []string
	RequiresAuth   bool
}

func main() {
	// 只接受一个可选 NAME 参数：传了就只扫该资源，没传扫全。
	var nameFilter string
	for _, arg := range os.Args[1:] {
		if arg == "" {
			continue
		}
		if nameFilter == "" {
			nameFilter = arg
		}
	}
	if nameFilter != "" && !camelCaseRe.MatchString(nameFilter) {
		fatal(fmt.Errorf("NAME=%q 必须 CamelCase 首字母大写", nameFilter))
	}

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		fatal(fmt.Errorf("chdir: %w", err))
	}

	groups, err := collectByResource(nameFilter)
	if err != nil {
		fatal(err)
	}

	var allFindings []finding
	for _, resource := range sortedKeys(groups) {
		if builtinResources[resource] {
			continue
		}
		ops := groups[resource]
		allFindings = append(allFindings, checkResource(resource, ops)...)
	}

	if len(allFindings) == 0 {
		if nameFilter != "" {
			fmt.Printf("new-endpoint-check: %s clean.\n", nameFilter)
		} else {
			fmt.Printf("new-endpoint-check: %d resource(s) clean.\n",
				countNonBuiltin(groups))
		}
		return
	}

	// 按资源 → severity → message 排序输出，让 git-friendly diff 稳定。
	sort.SliceStable(allFindings, func(i, j int) bool {
		if allFindings[i].resource != allFindings[j].resource {
			return allFindings[i].resource < allFindings[j].resource
		}
		if allFindings[i].severity != allFindings[j].severity {
			return severityRank(allFindings[i].severity) < severityRank(allFindings[j].severity)
		}
		return allFindings[i].message < allFindings[j].message
	})

	fmt.Fprintf(os.Stderr, "new-endpoint-check: %d finding(s):\n", len(allFindings))
	var curResource string
	for _, f := range allFindings {
		if f.resource != curResource {
			fmt.Fprintf(os.Stderr, "\n  resource: %s\n", f.resource)
			curResource = f.resource
		}
		fmt.Fprintf(os.Stderr, "    %s %s\n", severityTag(f.severity), f.message)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "fix: 跑 make new-endpoint NAME=<Resource>，或人工修复 yaml ↔ 代码漂移。")
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// resource collection
// ---------------------------------------------------------------------------

// collectByResource 解析 api/openapi.yaml，把所有 op 按资源归属分桶。归属
// 来源（按优先级，与 new-endpoint.go::belongsToResource 镜像）：
//
//  1. op-level x-resource —— 显式声明
//  2. path-level x-resource —— 该 path 下所有 verb 默认归属
//  3. fallback：扫 internal/handler/*.go 找现有 <Name>Handler 类型作为
//     known resources，对 op 用 belongsToResource(op, pathResource, known)
//     反向判定它属于哪个 known
//  4. 仍归不上 → 资源名取"Unknown_<operationId>"，让用户看到这条游离 op
//
// 第 3 步与 new-endpoint 的镜像：new-endpoint 给定 NAME 找 ops；check 扫
// 代码端 NAME 集合 + yaml 来比对。这样即便 yaml 没 x-resource，也能正确
// 把 enqueueExampleTask 归到 Example、把 createAuthToken 归到 Auth（builtin）
// 而不是猜出错误的 ExampleTask / AuthToken。
//
// nameFilter 非空时，只保留与 filter 同名的资源；空时返回所有。
func collectByResource(nameFilter string) (map[string][]operation, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile("api/openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("load api/openapi.yaml: %w", err)
	}

	known := scanKnownResources()

	groups := make(map[string][]operation)
	for path, item := range doc.Paths.Map() {
		pathResource := xResourceFromExtensions(item.Extensions)
		for verb, op := range item.Operations() {
			if op.OperationID == "" {
				continue
			}
			resource := resolveResourceName(op, pathResource, known)
			if nameFilter != "" && !strings.EqualFold(resource, nameFilter) {
				continue
			}

			pathParams, err := extractPathParams(path)
			if err != nil {
				// 多 path 参数的 op：new-endpoint 不能生成，但 check 仍纳入
				// 扫描——下游 router 检查会报 missing/mismatch。
				pathParams = collectAllPathParams(path)
			}

			groups[resource] = append(groups[resource], operation{
				OperationID:    op.OperationID,
				HandlerMethod:  deriveHandlerMethodSafe(op, resource),
				HTTPVerb:       strings.ToUpper(verb),
				Path:           path,
				IfaceMethod:    pascalize(op.OperationID),
				PathParamNames: pathParams,
				RequiresAuth:   requiresBearerAuth(op, doc),
			})
		}
	}

	for resource := range groups {
		sort.Slice(groups[resource], func(i, j int) bool {
			if groups[resource][i].Path != groups[resource][j].Path {
				return groups[resource][i].Path < groups[resource][j].Path
			}
			return groups[resource][i].HTTPVerb < groups[resource][j].HTTPVerb
		})
	}
	return groups, nil
}

// resolveResourceName 决定一条 op 归到哪个资源（详见 collectByResource doc
// comment 里的优先级）。
func resolveResourceName(op *openapi3.Operation, pathResource string, known []string) string {
	if r := xResourceFromExtensions(op.Extensions); r != "" {
		return r
	}
	if pathResource != "" {
		return pathResource
	}
	// 显式映射：builtin 里那些 op 名跟资源名不字面相关的（如 getLivez → Health）。
	if r, ok := builtinOpToResource[op.OperationID]; ok {
		return r
	}
	// fallback：扫 known resources（含 builtin），看哪个 NAME 的
	// operationId 包含规则能命中——和 new-endpoint.go::belongsToResource
	// 在 default 分支的逻辑一致。
	lowerID := strings.ToLower(op.OperationID)
	for _, name := range known {
		if strings.Contains(lowerID, strings.ToLower(name)) {
			return name
		}
	}
	return "Unknown_" + op.OperationID
}

// scanKnownResources 把代码端"已经存在的资源"收集起来作为 fallback 归属池：
//
//   - builtin（Auth / Health / OpenAPI）始终在
//   - 扫 internal/handler/*.go 找 type <X>Handler struct 声明
//
// 返回值按字符串长度降序——让 fallback 匹配时优先命中更长的资源名（避免
// "Order" 抢走 "OrderPayment" 的 op）。
func scanKnownResources() []string {
	set := make(map[string]bool)
	for builtin := range builtinResources {
		set[builtin] = true
	}
	entries, err := os.ReadDir("internal/handler")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			file := "internal/handler/" + e.Name()
			for _, name := range scanHandlerTypeNames(file) {
				set[name] = true
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		// 长名优先（避免前缀复合资源被短名吞掉）。
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	return names
}

// scanHandlerTypeNames 扫一个 handler/*.go 文件，返回所有形如 <Name>Handler
// 的 struct 名（去掉 Handler 后缀）。APIServer 这种不带 Handler 后缀的不算。
func scanHandlerTypeNames(file string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var names []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		if _, ok := ts.Type.(*ast.StructType); !ok {
			return true
		}
		if strings.HasSuffix(ts.Name.Name, "Handler") {
			name := strings.TrimSuffix(ts.Name.Name, "Handler")
			if name != "" {
				names = append(names, name)
			}
		}
		return true
	})
	return names
}

// deriveHandlerMethodSafe 复刻 new-endpoint.go::deriveHandlerMethod 的核心
// 逻辑，但失败时返回空串（不报错）——check 阶段允许 op 没法生成 handler，
// 走 Missing 而不是阻塞扫描。
func deriveHandlerMethodSafe(op *openapi3.Operation, resource string) string {
	if op.Extensions != nil {
		if raw, ok := op.Extensions["x-handler-method"]; ok {
			if s, ok := raw.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	id := op.OperationID
	for _, tryName := range []string{resource + "s", resource} {
		idx := strings.Index(strings.ToLower(id), strings.ToLower(tryName))
		if idx < 0 {
			continue
		}
		remaining := id[:idx] + id[idx+len(tryName):]
		if remaining == "" {
			continue
		}
		out := strings.ToUpper(remaining[:1]) + remaining[1:]
		if camelCaseRe.MatchString(out) {
			return out
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// per-resource checks
// ---------------------------------------------------------------------------

// checkResource 跑一个资源的 6 项漂移检查，返回所有 findings。
func checkResource(resource string, ops []operation) []finding {
	var out []finding
	lower := strings.ToLower(resource)

	handlerFile := fmt.Sprintf("internal/handler/%s.go", lower)
	serviceFile := fmt.Sprintf("internal/service/%s.go", lower)
	openapiFile := "internal/handler/openapi.go"
	routerFile := "internal/router/router.go"
	serverFile := "internal/server.go"
	routerTestFile := "internal/router/router_test.go"

	// 1. handler 文件 + 方法集
	handlerMethods := collectMethods(handlerFile, fmt.Sprintf("%sHandler", resource))
	if handlerMethods == nil {
		out = append(out, finding{
			resource, "missing",
			fmt.Sprintf("internal/handler/%s.go 不存在（%d 个 yaml op 无 handler）",
				lower, len(ops)),
		})
	} else {
		for _, op := range ops {
			if op.HandlerMethod == "" {
				continue // operationId 推不出方法名，等用户加 x-handler-method
			}
			if _, ok := handlerMethods[op.HandlerMethod]; !ok {
				out = append(out, finding{
					resource, "missing",
					fmt.Sprintf("%sHandler.%s 缺失（yaml: %s %s）",
						resource, op.HandlerMethod, op.HTTPVerb, op.Path),
				})
			}
		}
	}

	// 2. service 文件 + 方法集
	serviceMethods := collectMethods(serviceFile, fmt.Sprintf("%sService", resource))
	if serviceMethods == nil {
		out = append(out, finding{
			resource, "missing",
			fmt.Sprintf("internal/service/%s.go 不存在", lower),
		})
	} else {
		for _, op := range ops {
			if op.HandlerMethod == "" {
				continue
			}
			if _, ok := serviceMethods[op.HandlerMethod]; !ok {
				out = append(out, finding{
					resource, "missing",
					fmt.Sprintf("%sService.%s 缺失（handler 会调它）",
						resource, op.HandlerMethod),
				})
			}
		}
	}

	// 3. router 注册：扫 register<Resource>Routes 里的 .GET/.POST/...
	registerFn := fmt.Sprintf("register%sRoutes", resource)
	registeredRoutes := collectRegisteredRoutes(routerFile, registerFn)
	if registeredRoutes == nil {
		out = append(out, finding{
			resource, "missing",
			fmt.Sprintf("%s 在 %s 未定义", registerFn, routerFile),
		})
	} else {
		resourcePrefix := findResourcePrefix(ops, resource)
		for _, op := range ops {
			if op.HandlerMethod == "" {
				continue
			}
			expectedPath := expectedRoutePath(op.Path, resourcePrefix)
			matched := false
			for _, r := range registeredRoutes {
				if strings.EqualFold(r.verb, op.HTTPVerb) && r.handler == op.HandlerMethod {
					matched = true
					if r.pathKnown && r.path != expectedPath {
						out = append(out, finding{
							resource, "mismatch",
							fmt.Sprintf("router %s %s 注册路径为 %s",
								op.HTTPVerb, op.Path, r.path),
						})
					}
					// 鉴权分组检查：yaml 要求 bearerAuth 但路由挂在公开组（或反之）
					if op.RequiresAuth && !r.inAuthGroup {
						out = append(out, finding{
							resource, "mismatch",
							fmt.Sprintf("router %s %s 缺 AuthRequired（yaml 要求 bearerAuth）",
								op.HTTPVerb, op.Path),
						})
					} else if !op.RequiresAuth && r.inAuthGroup {
						out = append(out, finding{
							resource, "mismatch",
							fmt.Sprintf("router %s %s 挂在 AuthRequired 子组但 yaml 未声明 bearerAuth",
								op.HTTPVerb, op.Path),
						})
					}
					break
				}
			}
			if !matched {
				out = append(out, finding{
					resource, "missing",
					fmt.Sprintf("router 未注册 %s → %sHandler.%s",
						op.HTTPVerb, resource, op.HandlerMethod),
				})
			}
		}
		// 反向检查：register 里有但 yaml 不再有 → Stale
		yamlHandlers := make(map[string]bool)
		for _, op := range ops {
			if op.HandlerMethod != "" {
				yamlHandlers[op.HandlerMethod] = true
			}
		}
		for _, r := range registeredRoutes {
			if !yamlHandlers[r.handler] {
				out = append(out, finding{
					resource, "stale",
					fmt.Sprintf("router 注册了 %sHandler.%s 但 yaml 已无对应 op",
						resource, r.handler),
				})
			}
		}
	}

	// 4. APIServer 转发：对应 ServerInterface 的每个 op，需要一个 (s *APIServer).<IfaceMethod>
	apiserverMethods := collectMethods(openapiFile, "APIServer")
	for _, op := range ops {
		if _, ok := apiserverMethods[op.IfaceMethod]; !ok {
			out = append(out, finding{
				resource, "missing",
				fmt.Sprintf("APIServer.%s 转发方法缺失（oapi.ServerInterface 契约）",
					op.IfaceMethod),
			})
		}
	}

	// 5. server.go::HTTPHandlers 装配
	if !hasStructField(serverFile, "HTTPHandlers", resource) {
		out = append(out, finding{
			resource, "missing",
			fmt.Sprintf("internal/server.go 的 HTTPHandlers struct 没有 %s 字段",
				resource),
		})
	}

	// 6. router_test.go::buildEngine 的 deps fixture
	// 这个文件是测试，缺字段不一定挂——但 TestRouterCoversAllSpecOperations 会
	// 发现 404，提示作者补 fixture。这里做轻量检查：含资源名即可。
	if !fileContains(routerTestFile, resource+":") &&
		!fileContains(routerTestFile, resource+" :") {
		out = append(out, finding{
			resource, "missing",
			fmt.Sprintf("router_test.go 的 buildEngine deps fixture 没注入 %s",
				resource),
		})
	}

	return out
}

// ---------------------------------------------------------------------------
// AST helpers
// ---------------------------------------------------------------------------

// collectMethods 解析 file，返回类型 typeName 上的所有方法名集合。文件不存在
// 时返 nil（caller 用作"文件缺失"的信号）；解析失败时返 nil 并打 warning。
func collectMethods(file, typeName string) map[string]bool {
	if _, err := os.Stat(file); err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: parse %s: %v\n", file, err)
		return nil
	}
	methods := make(map[string]bool)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		recvType := exprName(fn.Recv.List[0].Type)
		if recvType == typeName {
			methods[fn.Name.Name] = true
		}
	}
	return methods
}

// exprName 把方法 receiver 类型表达式（可能是 *T 或 T）压成纯类型名 T。
func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return exprName(t.X)
	default:
		return ""
	}
}

// routerRegistration 是 register<Name>Routes 函数体里的一条 .GET / .POST /
// .PUT / .DELETE / .PATCH 调用——记录 verb / handler 方法名 / 是否在
// AuthRequired 子组内。
type routerRegistration struct {
	verb        string
	path        string
	pathKnown   bool
	handler     string // selector 末段，如 "List" (deps.Example.List → "List")
	inAuthGroup bool   // 调用前一个参数是 deps.AuthRequired？
}

type routerGroupInfo struct {
	path      string
	pathKnown bool
	auth      bool
}

// collectRegisteredRoutes 扫 router.go 里的 register<Resource>Routes 函数体，
// 提取每条 verb 调用（如 examples.GET("/x", deps.Example.List)）成
// routerRegistration。
//
// path 判定：尽量解析 `g := r.Group("/orders")` 这类字面量 group path，再
// 拼接 `.GET("/:id", ...)` 的第一个字面量参数；遇到自定义 helper / const path
// 时 pathKnown=false，避免误报。
//
// inAuthGroup 判定：
//   - 路由调用直接带 AuthRequired middleware（如 g.GET(..., deps.AuthRequired, h)）
//   - 或接收者是由 *.Group(... AuthRequired ...) 派生出来的子组
func collectRegisteredRoutes(file, fnName string) []routerRegistration {
	if _, err := os.Stat(file); err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: parse %s: %v\n", file, err)
		return nil
	}
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == fnName {
			fn = d
			break
		}
	}
	if fn == nil || fn.Body == nil {
		return nil
	}

	groups := collectRouterGroupInfo(fn)

	var routes []routerRegistration
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		verb := strings.ToUpper(sel.Sel.Name)
		switch verb {
		case "GET", "POST", "PUT", "DELETE", "PATCH":
		default:
			return true
		}
		// 末参是 handler 方法引用，如 deps.Example.List 或 h.Foo.Bar；
		// 用 selectorLastIdent 取最后一段 Ident。
		if len(call.Args) == 0 {
			return true
		}
		handlerName := selectorLastIdent(call.Args[len(call.Args)-1])
		if handlerName == "" {
			return true
		}
		group, groupKnown := groups[selectorString(sel.X)]
		path, pathKnown := routeCallPath(call, group, groupKnown)
		inAuth := group.auth
		if len(call.Args) >= 3 {
			for _, a := range call.Args[1 : len(call.Args)-1] {
				if strings.Contains(selectorString(a), "AuthRequired") {
					inAuth = true
					break
				}
			}
		}
		routes = append(routes, routerRegistration{
			verb:        verb,
			path:        path,
			pathKnown:   pathKnown,
			handler:     handlerName,
			inAuthGroup: inAuth,
		})
		return true
	})
	return routes
}

// collectRouterGroupInfo 找出 register<Resource>Routes 函数里的 gin RouterGroup
// 变量，记录它们的相对 path 与是否携带 AuthRequired。覆盖脚手架生成的：
//
//	authed := g.Group("", deps.AuthRequired)
//	authed.POST("", deps.Order.Create)
//
// 也覆盖从已鉴权 group 再派生子 group 的形态：
//
//	nested := authed.Group("/nested")
func collectRouterGroupInfo(fn *ast.FuncDecl) map[string]routerGroupInfo {
	groups := make(map[string]routerGroupInfo)
	if fn.Type != nil && fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				groups[name.Name] = routerGroupInfo{pathKnown: true}
			}
		}
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range stmt.Rhs {
				info, ok := routerGroupInfoFromExpr(rhs, groups)
				if !ok {
					continue
				}
				if i >= len(stmt.Lhs) {
					continue
				}
				if id, ok := stmt.Lhs[i].(*ast.Ident); ok {
					groups[id.Name] = info
				}
			}
		case *ast.ValueSpec:
			for i, rhs := range stmt.Values {
				info, ok := routerGroupInfoFromExpr(rhs, groups)
				if !ok {
					continue
				}
				if i >= len(stmt.Names) {
					continue
				}
				groups[stmt.Names[i].Name] = info
			}
		}
		return true
	})

	return groups
}

func routerGroupInfoFromExpr(expr ast.Expr, groups map[string]routerGroupInfo) (routerGroupInfo, bool) {
	if info, ok := groups[selectorString(expr)]; ok {
		return info, true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return routerGroupInfo{}, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Group" {
		return routerGroupInfo{}, false
	}

	info, ok := groups[selectorString(sel.X)]
	if !ok {
		info = routerGroupInfo{}
	}
	if len(call.Args) == 0 {
		info.pathKnown = false
	} else if p, ok := stringLiteral(call.Args[0]); ok && info.pathKnown {
		info.path = joinRoutePath(info.path, p)
	} else {
		info.pathKnown = false
	}
	for _, arg := range call.Args {
		if strings.Contains(selectorString(arg), "AuthRequired") {
			info.auth = true
			break
		}
	}
	return info, true
}

func routeCallPath(call *ast.CallExpr, group routerGroupInfo, groupKnown bool) (string, bool) {
	if !groupKnown || !group.pathKnown || len(call.Args) == 0 {
		return "", false
	}
	p, ok := stringLiteral(call.Args[0])
	if !ok {
		return "", false
	}
	return joinRoutePath(group.path, p), true
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// selectorLastIdent 取 selector 表达式（如 deps.Example.List）的末段 Ident
// 名字；不是 selector 时返空串。
func selectorLastIdent(e ast.Expr) string {
	if sel, ok := e.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// selectorString 把任意 selector / ident 表达式拍平成 "a.b.c" 字符串，仅
// 用于 inAuthGroup 启发式判断（containsXxx）。
func selectorString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return selectorString(t.X) + "." + t.Sel.Name
	default:
		return ""
	}
}

// hasStructField 检查 file 里 typeName 这个 struct 有没有名为 fieldName 的
// 字段。用于 internal/server.go::handlers struct 装配检查。
func hasStructField(file, typeName, fieldName string) bool {
	if _, err := os.Stat(file); err != nil {
		return false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != typeName {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return false
		}
		for _, field := range st.Fields.List {
			for _, name := range field.Names {
				if name.Name == fieldName {
					found = true
					return false
				}
			}
		}
		return false
	})
	return found
}

// fileContains 是文本级 grep——只在 AST 不便的场景用（如检查测试 fixture
// 含字段名）。
func fileContains(file, needle string) bool {
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

func findResourcePrefix(ops []operation, _ string) string {
	if len(ops) == 0 {
		return ""
	}
	prefix := ops[0].Path
	for _, op := range ops[1:] {
		prefix = commonPrefix(prefix, op.Path)
	}
	if idx := strings.LastIndex(prefix, "/"); idx >= 0 && idx < len(prefix)-1 {
		tail := prefix[idx+1:]
		if strings.HasPrefix(tail, "{") {
			prefix = prefix[:idx]
		}
	}
	return strings.TrimRight(prefix, "/")
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

func ginPath(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return ""
	}
	re := regexp.MustCompile(`\{([^}]+)\}`)
	return re.ReplaceAllString(rest, ":$1")
}

func expectedRoutePath(path, resourcePrefix string) string {
	groupPath := strings.TrimPrefix(resourcePrefix, "/api/v1")
	if groupPath == "" {
		groupPath = "/"
	}
	return joinRoutePath(groupPath, ginPath(path, resourcePrefix))
}

func joinRoutePath(base, child string) string {
	switch {
	case base == "" || base == "/":
		if child == "" {
			return "/"
		}
		if strings.HasPrefix(child, "/") {
			return child
		}
		return "/" + child
	case child == "":
		return base
	default:
		return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(child, "/")
	}
}

// ---------------------------------------------------------------------------
// yamlspec helpers (copied from scripts/new-endpoint.go — keep in sync!)
// ---------------------------------------------------------------------------

// xResourceFromExtensions 同 scripts/new-endpoint.go。
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

var pathParamRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// extractPathParams 同 scripts/new-endpoint.go（≥2 个参数返 error）。
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
		return nil, fmt.Errorf("path %q 含 %d 个参数", path, len(names))
	}
	return names, nil
}

// collectAllPathParams 不 fail-fast 的版本——给 check 路径用，多 path
// 参数也能扫，下游靠 router mismatch 报。
func collectAllPathParams(path string) []string {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

// requiresBearerAuth 同 scripts/new-endpoint.go。
func requiresBearerAuth(op *openapi3.Operation, doc *openapi3.T) bool {
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

// pascalize 把 camelCase / snake_case 化为 PascalCase（与 oapi-codegen 的
// operationId → Go 方法名映射对齐）。同 scripts/new-endpoint.go。
func pascalize(s string) string {
	if s == "" {
		return s
	}
	parts := regexp.MustCompile(`[_\-\s]+`).Split(s, -1)
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// misc
// ---------------------------------------------------------------------------

func sortedKeys(m map[string][]operation) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func countNonBuiltin(m map[string][]operation) int {
	n := 0
	for k := range m {
		if !builtinResources[k] {
			n++
		}
	}
	return n
}

func severityRank(s string) int {
	switch s {
	case "missing":
		return 0
	case "stale":
		return 1
	case "mismatch":
		return 2
	default:
		return 3
	}
}

func severityTag(s string) string {
	switch s {
	case "missing":
		return "[!]"
	case "stale":
		return "[~]"
	case "mismatch":
		return "[-]"
	default:
		return "[?]"
	}
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "new-endpoint-check:", err)
	os.Exit(1)
}
