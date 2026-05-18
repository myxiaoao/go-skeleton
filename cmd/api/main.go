package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"go-skeleton/config"
	app "go-skeleton/internal"
	"go-skeleton/internal/bootstrap"
	"go-skeleton/internal/router"
	"go-skeleton/pkg/buildinfo"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/sdnotify"
)

const (
	gracefulShutdownTimeout = 10 * time.Second
	serverExitWaitTimeout   = 2 * time.Second
)

var errServerExitTimeout = errors.New("web server exit wait timeout")

type httpServerLifecycle interface {
	Shutdown(context.Context) error
	Close() error
}

func main() {
	showVersion := flag.Bool("version", false, "print version info and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.String())
		os.Exit(0)
	}

	config.LoadEnv("cmd/api/.env")
	cfg, err := config.Load()
	if err != nil {
		panic(fmt.Sprintf("load config: %v", err))
	}
	if err := bootstrap.InitRuntime(cfg, "api"); err != nil {
		panic(fmt.Sprintf("init runtime: %v", err))
	}
	defer func() { _ = applog.Sync() }()

	registry, err := bootstrap.InitAPI(cfg)
	if err != nil {
		applog.L().Fatal("initialize application", zap.Error(err))
	}
	defer func() {
		if err := registry.Close(); err != nil {
			applog.L().Warn("close registry", zap.Error(err))
		}
	}()

	server, err := app.NewServer(registry)
	if err != nil {
		applog.L().Fatal("assemble http server", zap.Error(err))
	}

	// 可选的 pprof debug 服务器，仅在配置开启时启动。生产默认关闭；排障时
	// 临时打开 + 通过 SSH 隧道访问，**不要**把端口暴露到公网。
	var pprofSrv *http.Server
	if cfg.Server.PprofEnabled {
		pprofSrv = router.NewPprofServer(cfg.Server.PprofAddr)
		go func() {
			applog.L().Info("starting pprof server", zap.String("addr", cfg.Server.PprofAddr))
			if err := pprofSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				applog.L().Error("pprof server error", zap.Error(err))
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// sd_notify watchdog 心跳：systemd Type=notify + WatchdogSec 时，定期发
	// WATCHDOG=1 让 systemd 知道进程还活着；ctx 取消时自然退出。非 Linux 是 noop。
	go sdnotify.Watchdog(ctx, cfg.Server.WatchdogInterval)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run()
	}()

	applog.L().Info("starting web server", zap.String("addr", cfg.Server.Port))

	select {
	case err := <-errCh:
		if err != nil {
			applog.L().Fatal("web server error", zap.Error(err))
		}
		return
	case <-ctx.Done():
	}

	// graceful drain：翻 Registry.Draining 让 /health 立刻返 503，让 LB 在
	// 窗口期内摘流；之后再 Shutdown HTTP server。窗口由 GracefulDrain 控制，
	// 0 表示跳过 drain，直接 Shutdown（开发环境）。
	if registry.Draining != nil {
		registry.Draining.Store(true)
		if drain := cfg.Server.GracefulDrain; drain > 0 {
			applog.L().Info("draining: health 503 grace window", zap.Duration("window", drain))
			time.Sleep(drain)
		}
	}

	if err := shutdownWebServer(server, errCh, gracefulShutdownTimeout, serverExitWaitTimeout); err != nil {
		applog.L().Error("web server shutdown failed", zap.Error(err))
	}

	if pprofSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := pprofSrv.Shutdown(shutdownCtx); err != nil {
			applog.L().Warn("pprof shutdown failed", zap.Error(err))
		}
		cancel()
	}
}

func shutdownWebServer(server httpServerLifecycle, errCh <-chan error, shutdownTimeout, waitTimeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	forced := false
	if err := server.Shutdown(shutdownCtx); err != nil {
		forced = true
		applog.L().Error("web server shutdown error", zap.Error(err))
		if closeErr := server.Close(); closeErr != nil {
			applog.L().Error("web server force close error", zap.Error(closeErr))
		}
	}

	if exitErr := waitForServerExit(errCh, waitTimeout); exitErr != nil {
		if errors.Is(exitErr, errServerExitTimeout) {
			applog.L().Warn("web server exit wait timed out", zap.Error(exitErr))
		} else {
			applog.L().Error("web server exited with error", zap.Error(exitErr))
		}
		return exitErr
	}

	if forced {
		applog.L().Info("forced shutdown completed")
	} else {
		applog.L().Info("graceful shutdown completed")
	}
	return nil
}

func waitForServerExit(errCh <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-errCh:
		return err
	case <-timer.C:
		return fmt.Errorf("%w after %s", errServerExitTimeout, timeout)
	}
}
