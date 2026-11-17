package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("sociald stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	listener, handler, err := bindLocalHandler(cfg)
	if err != nil {
		return err
	}
	defer listener.Close()
	if closer, ok := handler.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	if background, ok := handler.(interface{ StartBackground(context.Context) error }); ok {
		if err := background.StartBackground(ctx); err != nil {
			_ = listener.Close()
			return err
		}
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	slog.Info("sociald listening", "address", listener.Addr().String(), "storage_configured", cfg.DatabasePath != "", "sending_enabled", false)
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		slog.Info("sociald stopped gracefully")
		return nil
	}
}

func bindLocalHandler(cfg config.Config) (net.Listener, http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	// Handler construction can permanently prune grants after an issuer change.
	// A duplicate startup that cannot own the port must not touch that state.
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, nil, err
	}
	handler, err := api.NewHandler(cfg)
	if err != nil {
		_ = listener.Close()
		return nil, nil, err
	}
	return listener, handler, nil
}
