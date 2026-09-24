package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTP HTTPConfig
	DB DBConfig
	LogLevel slog.Level
	ShutdownTimeout time.Duration
}

type HTTPConfig struct {
	Addr string
	ReadTimeout time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout time.Duration
	IdleTimeout time.Duration
}

type DBConfig struct {
	URL string
	MaxConns int32
	MinConns int32
	ConnectTimeout  time.Duration
	QueryTimeout    time.Duration
	MaxConnLifetime time.Duration
}

func Load() (Config, error) {
	var l loader

	cfg := Config{
		HTTP: HTTPConfig{
			Addr: l.required("HTTP_ADDR"),
			ReadTimeout: l.duration("HTTP_READ_TIMEOUT", "10s"),
			ReadHeaderTimeout: l.duration("HTTP_READ_HEADER_TIMEOUT", "5s"),
			WriteTimeout: l.duration("HTTP_WRITE_TIMEOUT", "10s"),
			IdleTimeout: l.duration("HTTP_IDLE_TIMEOUT", "60s"),
		},
		DB: DBConfig{
			URL: l.required("DATABASE_URL"),
			MaxConns: l.integer("DATABASE_MAX_CONNS", "10"),
			MinConns: l.integer("DATABASE_MIN_CONNS", "2"),
			ConnectTimeout: l.duration("DATABASE_CONNECT_TIMEOUT", "5s"),
			QueryTimeout: l.duration("DATABASE_QUERY_TIMEOUT", "3s"),
			MaxConnLifetime: l.duration("DATABASE_MAX_CONN_LIFETIME", "30m"),
		},
		ShutdownTimeout: l.duration("SHUTDOWN_TIMEOUT", "10s"),
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(l.optional("LOG_LEVEL", "info"))); err != nil {
		l.fail(fmt.Errorf("LOG_LEVEL: %w", err))
	}

	if cfg.DB.MaxConns <= 0 {
		l.fail(fmt.Errorf("DATABASE_MAX_CONNS must be > 0, got %d", cfg.DB.MaxConns))
	}
	if cfg.DB.MinConns < 0 || cfg.DB.MinConns > cfg.DB.MaxConns {
		l.fail(fmt.Errorf("DATABASE_MIN_CONNS must be in [0, DATABASE_MAX_CONNS], got %d", cfg.DB.MinConns))
	}

	if len(l.errs) > 0 {
		return Config{}, fmt.Errorf("invalid config: %w", errors.Join(l.errs...))
	}
	return cfg, nil
}

type loader struct{ errs []error }

func (l *loader) fail(err error) { l.errs = append(l.errs, err) }

func (l *loader) required(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		l.fail(fmt.Errorf("%s is required", key))
	}
	return v
}

func (l *loader) optional(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func (l *loader) duration(key, def string) time.Duration {
	raw := l.optional(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil {
		l.fail(fmt.Errorf("%s: invalid duration %q: %w", key, raw, err))
		return 0
	}
	if d <= 0 {
		l.fail(fmt.Errorf("%s must be positive, got %s", key, d))
	}
	return d
}

func (l *loader) integer(key, def string) int32 {
	raw := l.optional(key, def)
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		l.fail(fmt.Errorf("%s: invalid integer %q: %w", key, raw, err))
		return 0
	}
	return int32(n)
}
