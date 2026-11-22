package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/LuCh-Ans/template/internal/config"
)

func TestTelemetryTiming(t *testing.T) {
	const timeoutKey = "OTEL_EXPORTER_OTLP_TIMEOUT"
	setRequiredEnv(t)
	for _, tc := range []struct {
		name, timeout, delay, badKey string
		wantTimeout, wantDelay       time.Duration
	}{
		{"defaults", "", "", "", 2 * time.Second, time.Second},
		{"custom", "3500", "250", "", 3500 * time.Millisecond, 250 * time.Millisecond},
		{"zero timeout", "0", "1000", timeoutKey, 0, 0},
		{"negative delay", "2000", "-1", "OTEL_BSP_SCHEDULE_DELAY", 0, 0},
		{"invalid timeout", "2s", "1000", timeoutKey, 0, 0},
		{"overflow", "99999999999999999999", "1000", timeoutKey, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(timeoutKey, tc.timeout)
			t.Setenv("OTEL_BSP_SCHEDULE_DELAY", tc.delay)
			cfg, err := config.Load()
			if tc.badKey != "" {
				if err == nil || !strings.Contains(err.Error(), tc.badKey) {
					t.Fatalf("error = %v, want %s", err, tc.badKey)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Telemetry.ExportTimeout != tc.wantTimeout || cfg.Telemetry.BatchTimeout != tc.wantDelay {
				t.Fatalf("unexpected timing: %+v", cfg.Telemetry)
			}
		})
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("PUSH_HTTP_URL", "http://localhost:8090")
}

func TestPositionConfig(t *testing.T) {
	setRequiredEnv(t)
	const (
		checkInterval  = "POSITION_CHECK_INTERVAL"
		staleAfter     = "POSITION_STALE_AFTER"
		workers        = "POSITION_WORKERS"
		queueSize      = "POSITION_QUEUE_SIZE"
		requestTimeout = "PUSH_REQUEST_TIMEOUT"
		pushURL        = "PUSH_HTTP_URL"
	)

	for _, key := range []string{checkInterval, staleAfter, workers, queueSize, requestTimeout} {
		t.Setenv(key, "")
	}

	t.Run("defaults", func(t *testing.T) {
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		want := config.PositionsConfig{CheckInterval: 30 * time.Second, StaleAfter: time.Minute, Workers: 4, QueueSize: 100}
		if cfg.Positions != want || cfg.Push.RequestTimeout != time.Second {
			t.Fatalf("unexpected defaults: %+v, %+v", cfg.Positions, cfg.Push)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		t.Setenv(checkInterval, "5s")
		t.Setenv(staleAfter, "10s")
		t.Setenv(workers, "2")
		t.Setenv(queueSize, "8")
		t.Setenv(requestTimeout, "250ms")
		t.Setenv(pushURL, "https://push.example")
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		wantPositions := config.PositionsConfig{CheckInterval: 5 * time.Second, StaleAfter: 10 * time.Second, Workers: 2, QueueSize: 8}
		wantPush := config.PushConfig{HTTPURL: "https://push.example", RequestTimeout: 250 * time.Millisecond}
		if cfg.Positions != wantPositions || cfg.Push != wantPush {
			t.Fatalf("unexpected overrides: %+v, %+v", cfg.Positions, cfg.Push)
		}
	})

	for _, tc := range []struct{ key, value string }{
		{checkInterval, "0s"},
		{staleAfter, "bad"},
		{workers, "0"},
		{queueSize, "-1"},
		{requestTimeout, "-1s"},
		{pushURL, ""},
		{pushURL, "localhost:8090"},
		{pushURL, "ftp://push.example"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error = %v, want %s", err, tc.key)
			}
		})
	}
}
