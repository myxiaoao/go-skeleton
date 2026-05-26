package cache

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	applog "go-skeleton/pkg/log"
)

// RedisConfig 是 Redis 连接配置（地址 / 密码 / 逻辑 DB 编号 + 连接池）。
//
// PoolSize / MinIdleConns 透传给 go-redis；0 表示用库默认（PoolSize 默认
// 是 10 * GOMAXPROCS，单核机器约 10 个连接，高负载下容易卡在这上面没人
// 知道为什么，这两个旋钮就是给运维兜底用的）。
type RedisConfig struct {
	Addr         string
	Password     string
	DB           int
	PoolSize     int
	MinIdleConns int
}

// Client 是 *redis.Client 的薄封装，对外只暴露 Get / Set / Ping / Close
// 这些骨架真正用到的方法；需要更多原子操作时通过 Underlying() 取裸客户端。
type Client struct {
	rdb *redis.Client
}

// NewClient 构造 Redis 客户端，**不**做启动探活——探活由 bootstrap 层
// 统一调度（`internal/bootstrap.probeDependencies` + `STARTUP_PROBE_TIMEOUT`），
// 避免 NewClient 写死 5s 跟全局超时配置漂移。
//
// 配置校验仍在这里：addr 空直接返 error，让漏配快速发现；底层连接错误
// （密码错 / 网络不通）推迟到 caller 的 probe 阶段——pkg/cache 不持有
// "什么超时算合理"的话语权。
func NewClient(cfg RedisConfig) (*Client, error) {
	if strings.TrimSpace(cfg.Addr) == "" {
		return nil, fmt.Errorf("redis address is required")
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
	})

	applog.L().Info("redis client constructed", zap.String("addr", cfg.Addr), zap.Int("db", cfg.DB))
	return &Client{rdb: rdb}, nil
}

// Underlying 返回裸 *redis.Client，给需要 SET NX / 事务 / 管道等高级用法
// 的场景用。如果只用 Get / Set，调本封装的方法就行。
func (c *Client) Underlying() *redis.Client {
	if c == nil {
		return nil
	}
	return c.rdb
}

// Ping 探测 Redis 是否可达。/health 探针会调它，所以 ctx 应该带短超时。
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("redis client is not configured")
	}
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}

// Close 关闭底层连接。nil-safe，bootstrap.Registry.Close 会调它。
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Get 按 key 取字符串值。**key 不存在时返 "" + nil**（不当 error），调用方
// 用 val == "" 判 miss 即可，免去识别 redis.Nil 哨兵。
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	if c == nil || c.rdb == nil {
		return "", fmt.Errorf("redis client is not configured")
	}
	val, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis get %q: %w", key, err)
	}
	return val, nil
}

// Set 写一对 key / value，ttl 是过期时间；传 0 表示永不过期。
func (c *Client) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("redis client is not configured")
	}
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %q: %w", key, err)
	}
	return nil
}
