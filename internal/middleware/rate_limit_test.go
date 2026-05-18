package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"go-skeleton/pkg/errcode"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/response"
)

func init() {
	applog.SetLogger(zap.NewNop())
	gin.SetMode(gin.TestMode)
}

// 在 burst 内请求全部放行；超出返业务错误码 TooManyRequests。
// 响应 HTTP 状态仍 200（统一响应协议），用 body code 区分。
func TestIPRateLimiterAllowsWithinBurstThenBlocks(t *testing.T) {
	const burst = 3
	limiter := NewIPRateLimiterPerMinute(burst)
	if limiter == nil {
		t.Fatal("NewIPRateLimiterPerMinute returned nil for positive budget")
	}
	t.Cleanup(limiter.Stop)

	router := buildRateLimitRouter(limiter)

	// 前 burst 个请求应该全 200 + code=0。
	for i := 0; i < burst; i++ {
		w := serve(router, "1.2.3.4:5000")
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, w.Code)
		}
		if code := decodeCode(t, w); code != 0 {
			t.Fatalf("request %d: code = %d, want 0", i, code)
		}
	}

	// burst+1 应该被限流。
	w := serve(router, "1.2.3.4:5000")
	if code := decodeCode(t, w); code != errcode.TooManyRequests.Code() {
		t.Errorf("burst+1: code = %d, want %d", code, errcode.TooManyRequests.Code())
	}
}

// 不同 IP 各有独立 token bucket：同时打到 burst 不应相互影响。
func TestIPRateLimiterIsolatesByIP(t *testing.T) {
	const burst = 2
	limiter := NewIPRateLimiterPerMinute(burst)
	t.Cleanup(limiter.Stop)
	router := buildRateLimitRouter(limiter)

	// IP A 用满 burst。
	for i := 0; i < burst; i++ {
		if w := serve(router, "10.0.0.1:1"); decodeCode(t, w) != 0 {
			t.Fatalf("A req %d unexpectedly blocked", i)
		}
	}
	// A 第三发应该被限。
	if code := decodeCode(t, serve(router, "10.0.0.1:1")); code != errcode.TooManyRequests.Code() {
		t.Errorf("A burst+1: code = %d, want %d", code, errcode.TooManyRequests.Code())
	}
	// 同时 IP B 仍能正常通过 burst 个请求。
	for i := 0; i < burst; i++ {
		if w := serve(router, "10.0.0.2:1"); decodeCode(t, w) != 0 {
			t.Fatalf("B req %d unexpectedly blocked by A's exhaustion", i)
		}
	}
}

// NewIPRateLimiterPerMinute(0) 应返 nil 表示"不限流"；Middleware nil-safe，
// 调用方可以无条件 engine.Use(limiter.Middleware())。
func TestIPRateLimiterZeroDisables(t *testing.T) {
	limiter := NewIPRateLimiterPerMinute(0)
	if limiter != nil {
		t.Fatalf("NewIPRateLimiterPerMinute(0) = %v, want nil", limiter)
	}
	// nil limiter 的 Stop / Middleware 都不应该 panic。
	limiter.Stop()
	mw := limiter.Middleware()
	if mw == nil {
		t.Fatal("nil.Middleware() = nil, want a no-op handler")
	}
}

// cleanup 应该清掉超过 cutoff 的 visitor；保留 cutoff 之后访问的。
func TestIPRateLimiterCleanupRemovesStale(t *testing.T) {
	limiter := NewIPRateLimiterPerMinute(10)
	t.Cleanup(limiter.Stop)

	// 通过 allow 注入两个 visitor。
	limiter.allow("stale-ip")
	limiter.allow("fresh-ip")

	// 手动把 stale 的 lastSeen 调到过去。
	limiter.mu.Lock()
	limiter.visitors["stale-ip"].lastSeen = time.Now().Add(-10 * time.Minute)
	limiter.mu.Unlock()

	limiter.cleanup(time.Now().Add(-3 * time.Minute))

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if _, ok := limiter.visitors["stale-ip"]; ok {
		t.Error("stale-ip should have been pruned")
	}
	if _, ok := limiter.visitors["fresh-ip"]; !ok {
		t.Error("fresh-ip should have survived")
	}
}

// Stop 后 cleanupLoop 协程必须退出，否则长跑会泄漏 goroutine。
// 用 runtime.NumGoroutine 等到 goroutine 启动后再 Stop，最后断言归位。
func TestIPRateLimiterStopReleasesGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()
	limiter := NewIPRateLimiterPerMinute(5)

	// 等 cleanupLoop 真正被调度起来；最多等 1s（CI 慢机有 margin）。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() > before {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runtime.NumGoroutine() <= before {
		t.Fatalf("cleanupLoop goroutine did not start: before=%d now=%d",
			before, runtime.NumGoroutine())
	}

	limiter.Stop()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return // 退出成功
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("cleanupLoop did not exit after Stop: now=%d before=%d",
		runtime.NumGoroutine(), before)
}

// 重复 Stop 不应该 panic（双 close chan 经典坑）。
func TestIPRateLimiterStopIsIdempotent(t *testing.T) {
	limiter := NewIPRateLimiterPerMinute(5)
	limiter.Stop()
	// 第二次不应 panic。
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Stop panicked on second call: %v", r)
		}
	}()
	limiter.Stop()
}

// helpers ----------------------------------------------------------------

func buildRateLimitRouter(l *IPRateLimiter) *gin.Engine {
	r := gin.New()
	r.Use(l.Middleware())
	r.GET("/ping", func(c *gin.Context) {
		response.WriteSuccess(c, gin.H{"ok": true})
	})
	// 不能依赖 RemoteAddr 默认；显式设 TrustedProxies 让 c.ClientIP() 走 RemoteAddr。
	_ = r.SetTrustedProxies(nil)
	return r
}

func serve(router *gin.Engine, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func decodeCode(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var body response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	return body.Code
}
