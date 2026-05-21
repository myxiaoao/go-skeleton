package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-skeleton/config"
)

// defaultDocsConfig 是测试用的合法默认配置，对齐 config.go 的默认值。
func defaultDocsConfig() config.DocsConfig {
	return config.DocsConfig{Title: "API Docs", Theme: "system", Layout: "sidebar"}
}

func TestOpenAPISpecReturnsValidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/openapi.json", NewOpenAPIHandler(defaultDocsConfig()).Spec)

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct == "" || ct[:16] != "application/json" {
		t.Fatalf("expected application/json content-type, got %q", ct)
	}

	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if v, _ := spec["openapi"].(string); v == "" {
		t.Fatalf("expected openapi field in spec, got %v", spec["openapi"])
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected paths object in spec, got %T", spec["paths"])
	}
	for _, p := range []string{"/health", "/api/v1/examples", "/openapi.json"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("expected path %s in spec", p)
		}
	}
}

// renderDocs 用给定配置构造 handler 并请求 /docs，返回 recorder。
func renderDocs(t *testing.T, docs config.DocsConfig) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/docs", NewOpenAPIHandler(docs).Docs)

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDocs(t *testing.T) {
	w := renderDocs(t, defaultDocsConfig())

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content-type, got %q", ct)
	}

	body := w.Body.String()
	// 静态骨架 + 运行时 fetch /openapi.json 并改写 servers + 自动鉴权拦截器
	// + 锁定的 CDN 版本。
	for _, want := range []string{
		"elements-api",
		"fetch('/openapi.json'",
		"window.location.origin",
		"apiDescriptionDocument",
		"go_skeleton_token",
		"@stoplight/elements@" + elementsVersion,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected docs body to contain %q", want)
		}
	}
}

func TestDocsConfigRendering(t *testing.T) {
	tests := []struct {
		name      string
		docs      config.DocsConfig
		wantHas   []string
		wantNotHi []string
	}{
		{
			name: "system theme injects prefers-color-scheme follower",
			docs: config.DocsConfig{Title: "API Docs", Theme: "system", Layout: "sidebar"},
			wantHas: []string{
				`data-theme="system"`,
				"prefers-color-scheme: dark",
				`layout="sidebar"`,
				"<title>API Docs</title>",
			},
		},
		{
			name: "dark theme has no follower script",
			docs: config.DocsConfig{Title: "API Docs", Theme: "dark", Layout: "sidebar"},
			wantHas: []string{
				`data-theme="dark"`,
			},
			wantNotHi: []string{
				"prefers-color-scheme: dark",
			},
		},
		{
			name: "custom title/layout/logo and hide flags",
			docs: config.DocsConfig{
				Title: "ACME API", Theme: "light", Layout: "stacked",
				Logo: "https://example.com/logo.svg", HideTryIt: true, HideSchemas: true,
			},
			wantHas: []string{
				"<title>ACME API</title>",
				`layout="stacked"`,
				`logo="https://example.com/logo.svg"`,
				`hideTryIt="true"`,
				`hideSchemas="true"`,
			},
		},
		{
			name: "defaults do not emit optional attributes",
			docs: config.DocsConfig{Title: "API Docs", Theme: "light", Layout: "sidebar"},
			wantNotHi: []string{
				"hideTryIt=",
				"hideSchemas=",
				"logo=",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := renderDocs(t, tt.docs).Body.String()
			for _, want := range tt.wantHas {
				if !strings.Contains(body, want) {
					t.Errorf("expected body to contain %q", want)
				}
			}
			for _, notWant := range tt.wantNotHi {
				if strings.Contains(body, notWant) {
					t.Errorf("expected body NOT to contain %q", notWant)
				}
			}
		})
	}
}
