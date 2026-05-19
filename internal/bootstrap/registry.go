package bootstrap

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/hibiken/asynq"

	"go-skeleton/config"
	"go-skeleton/internal/taskqueue"
	"go-skeleton/pkg/auth"
	"go-skeleton/pkg/cache"
	"go-skeleton/pkg/database"
)

// Registry 持有进程启动期一次性初始化、整个生命周期共享的运行时资源。
// API / Worker / Migrate 三个进程各自有 InitXxx 装配自己需要的字段，没
// 用到的字段保持 nil。Close 统一释放，按"后开先关"顺序。
type Registry struct {
	Cfg       *config.Config
	DB        *database.DBManager
	Cache     *cache.Client
	Auth      *auth.JWTManager
	Queue     *taskqueue.Queue
	Inspector *asynq.Inspector

	// Draining 是 graceful shutdown 的进程级信号：收到 SIGTERM 后置 true，
	// /health 探活立即返 503，让 LB/K8s 在窗口内摘流。零值 false 即正常。
	Draining *atomic.Bool

	queueClient *asynq.Client
}

// newRegistry 构造 Registry 时统一初始化 Draining，避免散在各 InitXxx 里漏初始化。
func newRegistry() *Registry {
	return &Registry{Draining: &atomic.Bool{}}
}

// Close 释放 Registry 持有的所有资源。错误用 errors.Join 聚合返回——某条
// 失败不影响后续资源关闭，避免一处异常导致连接泄漏。关闭顺序：先 queue
// 客户端（停止入队） → cache → database（让正在写的事务先完成）。
func (r *Registry) Close() error {
	if r == nil {
		return nil
	}

	var errs []error
	if r.Inspector != nil {
		if err := r.Inspector.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close queue inspector: %w", err))
		}
	}
	if r.queueClient != nil {
		if err := r.queueClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close queue client: %w", err))
		}
	}
	if r.Cache != nil {
		if err := r.Cache.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close cache: %w", err))
		}
	}
	if r.DB != nil {
		if err := r.DB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close database: %w", err))
		}
	}
	return errors.Join(errs...)
}

// initDatabase 从 cfg 翻译出 database.Config 后建 GORM 实例。DSN 为空时
// database.Init 会返回带 nil DB 的 manager，由上层 InitAPI / InitWorker
// 决定是否致命。
func initDatabase(cfg *config.Config) (*database.DBManager, error) {
	return database.Init(database.Config{
		DSN:             cfg.Postgres.DSN,
		LogLevel:        cfg.Postgres.LogLevel,
		MaxIdleConns:    cfg.Postgres.MaxIdleConns,
		MaxOpenConns:    cfg.Postgres.MaxOpenConns,
		ConnMaxLifetime: cfg.Postgres.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Postgres.ConnMaxIdleTime,
	})
}

// initCache 在 REDIS_ADDR 配置时构造 cache 客户端；否则返回 nil（不报错），
// 让仅依赖 DB 的部署形态可以不带 Redis。
func initCache(cfg *config.Config) (*cache.Client, error) {
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		return nil, nil
	}
	return cache.NewClient(cache.RedisConfig{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.CacheDB,
		PoolSize:     cfg.Redis.PoolSize,
		MinIdleConns: cfg.Redis.MinIdleConns,
	})
}

// initAuth 在 JWT_SECRET 配置时构造 JWTManager；否则返回 nil 让
// middleware.BearerAuth 走 UNAUTHORIZED 兜底分支（不致命）。
func initAuth(cfg *config.Config) (*auth.JWTManager, error) {
	if strings.TrimSpace(cfg.Auth.JWTSecret) == "" {
		return nil, nil
	}
	return auth.NewJWTManager(auth.JWTConfig{
		Secret: cfg.Auth.JWTSecret,
		Issuer: cfg.Auth.JWTIssuer,
		TTL:    cfg.Auth.JWTTTL,
	})
}

// newAsynqClient 在 Redis 配置就绪时构造 asynq 入队客户端，用 QueueDB 编号
// 区别于 cache（cfg.Redis.CacheDB）；没配 Redis 返回 nil，让上层 taskqueue
// 包认作 unavailable。
func newAsynqClient(cfg *config.Config) *asynq.Client {
	if cfg == nil || strings.TrimSpace(cfg.Redis.Addr) == "" {
		return nil
	}
	return asynq.NewClient(AsynqRedisOpt(cfg))
}

// newAsynqInspector 构造一个 read-only Inspector，给 metrics 周期采集队列
// 状态用（pkg/metrics.StartAsynqCollector）。没配 Redis 返回 nil；调用方
// 用 nil 短路即可。Inspector 与 queueClient 各自持有独立连接池，互不影响。
func newAsynqInspector(cfg *config.Config) *asynq.Inspector {
	if cfg == nil || strings.TrimSpace(cfg.Redis.Addr) == "" {
		return nil
	}
	return asynq.NewInspector(AsynqRedisOpt(cfg))
}

// AsynqRedisOpt 把 cfg.Redis 翻译成 asynq.RedisClientOpt（用 QueueDB）。
// API 进程的 client / inspector 与 Worker 进程的 server 都从这里取连接参数，
// 让"队列连哪个 Redis"在 API 端和 Worker 端绑死一份定义。
func AsynqRedisOpt(cfg *config.Config) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.QueueDB,
	}
}
