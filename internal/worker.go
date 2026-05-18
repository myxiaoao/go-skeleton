package app

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
	"golang.org/x/sync/errgroup"

	"go-skeleton/internal/bootstrap"
	"go-skeleton/internal/worker"
)

// Worker owns the async task runtime created from application dependencies.
type Worker struct {
	server *asynq.Server
	mux    *asynq.ServeMux
}

// NewWorker wires async task handlers and the worker runtime.
func NewWorker(reg *bootstrap.Registry) (*Worker, error) {
	if err := validateWorkerRegistry(reg); err != nil {
		return nil, err
	}

	deps := buildWorkerDeps(reg)
	mux := asynq.NewServeMux()
	worker.RegisterHandlers(mux, deps)

	return &Worker{
		server: worker.NewServer(
			worker.NewRedisOpt(reg.Cfg.Redis.Addr, reg.Cfg.Redis.Password, reg.Cfg.Redis.QueueDB),
			worker.ServerConfig{
				Concurrency:    reg.Cfg.Worker.Concurrency,
				Queues:         reg.Cfg.Worker.Queues,
				RetryBaseDelay: reg.Cfg.Worker.RetryBaseDelay,
				RetryMaxDelay:  reg.Cfg.Worker.RetryMaxDelay,
			},
		),
		mux: mux,
	}, nil
}

// Run starts the task server until the context is canceled.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.server == nil || w.mux == nil {
		return errNilWorker
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		if err := w.server.Run(w.mux); err != nil {
			return fmt.Errorf("run worker server: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		<-groupCtx.Done()
		// Two-phase stop per Asynq docs: Stop halts pulling new tasks,
		// Shutdown waits for in-flight tasks to finish (bounded by
		// Config.ShutdownTimeout, 8s by default). Skipping Stop would let
		// new tasks slip in during the shutdown window and get rescheduled.
		w.server.Stop()
		w.server.Shutdown()
		return nil
	})

	return group.Wait()
}

func validateWorkerRegistry(reg *bootstrap.Registry) error {
	switch {
	case reg == nil:
		return errNilRegistry
	case reg.Cfg == nil:
		return errNilConfig
	case reg.Cfg.Redis.Addr == "":
		return fmt.Errorf("app: missing redis address")
	default:
		return nil
	}
}

func buildWorkerDeps(reg *bootstrap.Registry) *worker.Deps {
	// 示意：业务任务需要 DB 时，在这里组装 service：
	//
	//   var exampleSvc worker.ExampleProcessor
	//   if reg.DB != nil {
	//       repo := repository.NewExampleRepository(reg.DB.DB())
	//       exampleSvc = service.NewExampleService(repo, reg.Queue)
	//   }
	//
	// 然后把 exampleSvc 挂到 Deps.Example。worker 包本身不 import gorm。
	return &worker.Deps{
		Cache: reg.Cache,
		Queue: reg.Queue,
	}
}
