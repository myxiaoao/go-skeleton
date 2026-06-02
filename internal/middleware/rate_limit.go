package middleware

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"go-skeleton/pkg/errcode"
	"go-skeleton/pkg/response"
)

// visitor 记录单个 IP 的令牌桶状态 + 最近一次访问时间。lastSeen 给
// cleanupLoop 用来回收长时间不活跃的 IP，避免 visitors map 无限增长。
type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// IPRateLimiter 是基于内存的 per-IP 令牌桶限流器。
//
// 故意不用 Redis：单实例足够覆盖中等规模业务；跨实例分布式限流引入额外
// 网络 RTT + Redis 单点风险，骨架不预判需要。真要分布式限流再换底。
type IPRateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     rate.Limit
	burst    int
	stop     chan struct{}
	// done 在 cleanupLoop 退出时 close，给测试 / 优雅关闭一个确定性的"协程已
	// 退出"信号，避免靠 runtime.NumGoroutine() 这种全局计数做脆弱断言。
	done chan struct{}
}

// NewIPRateLimiterPerMinute 按"每分钟 requestsPerMinute 次"构造限流器。
//
// requestsPerMinute <= 0 时返回 nil，让上层 if rl != nil { engine.Use(...) }
// 干净跳过；burst 设成 requestsPerMinute 允许短时突发，符合直觉。
func NewIPRateLimiterPerMinute(requestsPerMinute int) *IPRateLimiter {
	if requestsPerMinute <= 0 {
		return nil
	}
	limiter := &IPRateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate.Every(time.Minute / time.Duration(requestsPerMinute)),
		burst:    requestsPerMinute,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go limiter.cleanupLoop()
	return limiter
}

// Middleware 拦截超出 per-IP 预算的请求，返 TOO_MANY_REQUESTS 错误信封。
// l 为 nil 时（限流关闭）直接放行，调用方可以无脑挂。
func (l *IPRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if l == nil || l.allow(c.ClientIP()) {
			c.Next()
			return
		}
		response.AbortError(c, errcode.TooManyRequests)
	}
}

// Stop 关停后台清理 goroutine 并等它真正退出。幂等：用 select 探测 stop
// channel 是否已 close，避免重复 close 触发 panic；之后等 done（cleanupLoop
// 退出时 close）确保返回后协程已结束，不留泄漏。Server.Shutdown 会调它释放资源。
func (l *IPRateLimiter) Stop() {
	if l == nil {
		return
	}
	select {
	case <-l.stop:
	default:
		close(l.stop)
	}
	<-l.done
}

// allow 给 IP 申请一个令牌；首次访问时懒构造 visitor。所有路径都更新
// lastSeen，让 cleanup 能正确判断"近期还在用"。
func (l *IPRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	v, ok := l.visitors[ip]
	if !ok {
		v = &visitor{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.visitors[ip] = v
	}
	v.lastSeen = time.Now()
	return v.limiter.Allow()
}

// cleanupLoop 每分钟扫一次 visitors，回收 3 分钟内没访问的 IP。窗口选 3
// 分钟而不是 1 分钟，是给瞬时跌零又回来的客户端留点缓冲，避免频繁重建
// rate.Limiter 把刚消的令牌补满。
func (l *IPRateLimiter) cleanupLoop() {
	defer close(l.done)

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.cleanup(time.Now().Add(-3 * time.Minute))
		case <-l.stop:
			return
		}
	}
}

// cleanup 删除 lastSeen < cutoff 的 visitor 条目，避免 visitors map 因为
// 单次访问的 IP 而无限增长。
func (l *IPRateLimiter) cleanup(cutoff time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, v := range l.visitors {
		if v.lastSeen.Before(cutoff) {
			delete(l.visitors, ip)
		}
	}
}
