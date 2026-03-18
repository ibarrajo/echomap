package main

import (
	"context"
	"fmt"
	"net"

	"github.com/elninja/echomap/internal/challenge"
	"github.com/elninja/echomap/internal/config"
	"github.com/elninja/echomap/internal/geo"
	"github.com/elninja/echomap/internal/grpcserver"
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
			geo.NewEngine,
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

func provideHandler(cfg config.Config, mgr *challenge.Manager, engine *geo.Engine) *grpcserver.Handler {
	return grpcserver.NewHandler(cfg, mgr, engine)
}

func provideGRPCServer() *grpc.Server {
	return grpc.NewServer()
}

func startServer(lc fx.Lifecycle, srv *grpc.Server, handler *grpcserver.Handler, cfg config.Config, logger *zap.Logger) {
	echomapv1.RegisterEchoMapServer(srv, handler)
	reflection.Register(srv)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			addr := fmt.Sprintf(":%d", cfg.GRPCPort)
			lis, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}
			logger.Info("EchoMap gRPC server starting", zap.String("addr", addr))
			go srv.Serve(lis)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("EchoMap gRPC server stopping")
			srv.GracefulStop()
			return nil
		},
	})
}
