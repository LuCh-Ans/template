package main

import (
	"log/slog"
	"os"
	"github.com/LuCh-Ans/template/internal/config"
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

	logger.Info("config loaded",
		"http_addr", cfg.HTTP.Addr,
		"log_level", cfg.LogLevel.String(),
		"shutdown_timeout", cfg.ShutdownTimeout.String(),
	)
	return nil
}
