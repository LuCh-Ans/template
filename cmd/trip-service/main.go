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
	"github.com/LuCh-Ans/template/internal/container"
	"github.com/LuCh-Ans/template/internal/infra/telemetry"
)

func main() {
	if err := run(); err != nil {
		telemetry.NewLogger(os.Stdout, slog.LevelInfo, "trip-service").Error("service stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	// Загрузка конфигурации, читаем переменные окружения
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	app, err := container.New(ctx, cfg)
	if err != nil {
		return err
	}

	logger := app.Logger
	srv := app.Server

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := shutdown(shutdownCtx, srv, app.Close); err != nil {
			runErr = errors.Join(runErr, err)
			return
		}
		logger.Info("service stopped")
	}()

	// Запуск сервера
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server started", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Ждём либо сигнал остановки, либо падение сервера
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		return nil
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	}
}
