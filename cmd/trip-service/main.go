package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/LuCh-Ans/template/internal/config"
	"github.com/LuCh-Ans/template/internal/httpapi"
	"github.com/LuCh-Ans/template/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("service stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	// Контекст жизни приложения: отменяется по SIGINT (Ctrl+C) или SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close() // закрывается последним — после остановки сервера
	logger.Info("connected to database")

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           httpapi.NewRouter(httpapi.NewHandler(pool, logger)),
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server started", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Ждём одно из двух: сигнал остановки или падение сервера
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	}

	// Новый контекст: ctx уже отменён, с ним Shutdown завершился бы мгновенно
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// Shutdown: перестаёт принимать новые соединения и ждёт завершения текущих запросов
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown timed out, forcing close", "error", err)
		_ = srv.Close()
	}

	logger.Info("service stopped")
	return nil
}
