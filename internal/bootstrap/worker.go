package bootstrap

import (
	"errors"
	"fmt"
	"strings"

	"go-skeleton/config"
	"go-skeleton/internal/taskqueue"
	"go-skeleton/pkg/cache"
	"go-skeleton/pkg/database"
)

// InitWorker 装配异步 worker 进程需要的全部资源（Cache / 可选 DB / Queue）。
//
// 顺序：先打开依赖 → 立刻做 fail-fast 探针 → 再装队列。
// Worker 必须能连 Redis；DB 可选（只有需要的 handler 才依赖）。
func InitWorker(cfg *config.Config) (*Registry, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		return nil, errors.New("redis address is required for worker")
	}

	var cleanups []func() error

	cacheClient, err := cache.NewClient(cache.RedisConfig{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.CacheDB,
		PoolSize:     cfg.Redis.PoolSize,
		MinIdleConns: cfg.Redis.MinIdleConns,
	})
	if err != nil {
		return nil, fmt.Errorf("init worker cache: %w", err)
	}
	cleanups = append(cleanups, cacheClient.Close)

	var dbMgr *database.DBManager
	if strings.TrimSpace(cfg.Postgres.DSN) != "" {
		dbMgr, err = initDatabase(cfg)
		if err != nil {
			runCleanups(cleanups)
			return nil, fmt.Errorf("init worker database: %w", err)
		}
		cleanups = append(cleanups, dbMgr.Close)
	}

	// 避免 typed-nil 把 interface 包成 non-nil：dbMgr 可能为 nil。
	var dbProbe dbPinger
	if dbMgr != nil {
		dbProbe = dbMgr
	}
	if err := probeDependencies(cfg, dbProbe, cacheClient); err != nil {
		runCleanups(cleanups)
		return nil, err
	}

	queueClient := newAsynqClient(cfg)
	reg := newRegistry()
	reg.Cfg = cfg
	reg.DB = dbMgr
	reg.Cache = cacheClient
	reg.Queue = taskqueue.NewQueue(queueClient)
	reg.queueClient = queueClient
	return reg, nil
}
