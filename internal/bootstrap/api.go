package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go-skeleton/config"
	"go-skeleton/internal/taskqueue"
	"go-skeleton/pkg/validator"
)

// InitAPI 装配 HTTP API 进程需要的全部资源（DB / Cache / Auth / Queue）。
//
// 顺序：先打开依赖 → 立刻做 fail-fast 探针 → 再装配上层。
// 任何中间步骤失败都跑一遍 cleanups（LIFO）释放已开资源、返回 error。
func InitAPI(cfg *config.Config) (*Registry, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	// cleanups 按"后开先关"累积，失败分支统一调 runCleanups。比每个错误分
	// 支重复写一堆 closeQuiet 干净，也少漏关一个资源的风险。
	var cleanups []func() error

	dbMgr, err := initDatabase(cfg)
	if err != nil {
		return nil, fmt.Errorf("init database: %w", err)
	}
	if dbMgr.DB() == nil {
		return nil, errors.New("postgres dsn is required for api")
	}
	cleanups = append(cleanups, dbMgr.Close)

	cacheClient, err := initCache(cfg)
	if err != nil {
		runCleanups(cleanups)
		return nil, fmt.Errorf("init cache: %w", err)
	}
	if cacheClient != nil {
		cleanups = append(cleanups, cacheClient.Close)
	}

	var cacheProbe cachePinger
	if cacheClient != nil {
		cacheProbe = cacheClient
	}
	if err := probeDependencies(cfg, dbMgr, cacheProbe); err != nil {
		runCleanups(cleanups)
		return nil, err
	}

	authManager, err := initAuth(cfg)
	if err != nil {
		runCleanups(cleanups)
		return nil, fmt.Errorf("init auth: %w", err)
	}

	validator.InitValidator()
	queueClient := newAsynqClient(cfg)

	reg := newRegistry()
	reg.Cfg = cfg
	reg.DB = dbMgr
	reg.Cache = cacheClient
	reg.Auth = authManager
	reg.Queue = taskqueue.NewQueue(queueClient)
	reg.queueClient = queueClient
	return reg, nil
}

// probeDependencies 在 bootstrap 末尾做依赖探活；DB 必探，Cache 仅在配置了 Addr 时探。
func probeDependencies(cfg *config.Config, db dbPinger, cache cachePinger) error {
	timeout := cfg.Server.StartupProbeTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	if db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := db.Ping(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("startup probe: postgres unreachable: %w", err)
		}
	}
	if cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := cache.Ping(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("startup probe: redis unreachable: %w", err)
		}
	}
	return nil
}

type dbPinger interface {
	Ping(context.Context) error
}

type cachePinger interface {
	Ping(context.Context) error
}

// runCleanups 按 LIFO（后开先关）调用累积的清理函数。fail-fast 路径用它
// 释放已开资源，nil 元素与单次失败都被静默吞掉——这是 init 失败兜底，
// 不需要再传播。
func runCleanups(cleanups []func() error) {
	for i := len(cleanups) - 1; i >= 0; i-- {
		if cleanups[i] != nil {
			_ = cleanups[i]()
		}
	}
}
