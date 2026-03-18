package main

import (
	"context"
	"fmt"
	"net"

	"github.com/elninja/echomap/internal/challenge"
	"github.com/elninja/echomap/internal/config"
	"github.com/elninja/echomap/internal/dataset"
	"github.com/elninja/echomap/internal/geo"
	"github.com/elninja/echomap/internal/grpcserver"
	"github.com/elninja/echomap/internal/ratelimit"
	"github.com/elninja/echomap/internal/storage"
	echomapv1 "github.com/elninja/echomap/proto/v1"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	fx.New(
		fx.Provide(
			config.New,
			provideLogger,
			provideChallengeManager,
			provideEngine,
			provideStorage,
			provideRateLimiter,
			provideGRPCServer,
			provideHandler,
		),
		fx.Invoke(startServer),
	).Run()
}

func provideLogger() (*zap.Logger, error) {
	return zap.NewProduction()
}

func provideChallengeManager(cfg config.Config) *challenge.Manager {
	return challenge.NewManager(cfg.HMACSecret, cfg.TokenTTL)
}

func provideEngine(cfg config.Config, logger *zap.Logger) *geo.Engine {
	var opts []geo.EngineOption

	if cfg.DatasetPath != "" {
		ds, err := dataset.LoadCSV(cfg.DatasetPath)
		if err != nil {
			logger.Warn("failed to load dataset, running without soft bounds",
				zap.String("path", cfg.DatasetPath), zap.Error(err))
		} else {
			opts = append(opts, geo.WithDataset(ds))
			logger.Info("loaded latency dataset",
				zap.String("path", cfg.DatasetPath),
				zap.Int("entries", ds.EntryCount()),
				zap.Int("cities", len(ds.Cities())))
		}
	}

	return geo.NewEngine(opts...)
}

func provideStorage(cfg config.Config, logger *zap.Logger) *storage.Repository {
	repo, err := storage.New(cfg.DBPath)
	if err != nil {
		logger.Fatal("failed to open database", zap.String("path", cfg.DBPath), zap.Error(err))
	}
	logger.Info("database opened", zap.String("path", cfg.DBPath))
	return repo
}

func provideRateLimiter(cfg config.Config) *ratelimit.Limiter {
	return ratelimit.New(cfg.RateLimitMax, cfg.RateLimitWindow)
}

func provideHandler(cfg config.Config, mgr *challenge.Manager, engine *geo.Engine, store *storage.Repository) *grpcserver.Handler {
	return grpcserver.NewHandler(cfg, mgr, engine).WithStorage(store)
}

func provideGRPCServer(lim *ratelimit.Limiter) *grpc.Server {
	return grpc.NewServer(
		grpc.UnaryInterceptor(ratelimit.UnaryInterceptor(lim)),
	)
}

func startServer(lc fx.Lifecycle, srv *grpc.Server, handler *grpcserver.Handler, store *storage.Repository, cfg config.Config, logger *zap.Logger) {
	echomapv1.RegisterEchoMapServer(srv, handler)
	reflection.Register(srv)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			addr := fmt.Sprintf(":%d", cfg.GRPCPort)
			lis, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}
			logger.Info("EchoMap gRPC server starting",
				zap.String("addr", addr),
				zap.Int("rate_limit", cfg.RateLimitMax),
				zap.Duration("rate_window", cfg.RateLimitWindow))
			go srv.Serve(lis)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("EchoMap gRPC server stopping")
			srv.GracefulStop()
			store.Close()
			return nil
		},
	})
}
