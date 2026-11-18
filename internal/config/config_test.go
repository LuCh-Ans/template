package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/LuCh-Ans/template/internal/config"
)

func TestTelemetryTiming(t *testing.T) {
	const timeoutKey = "OTEL_EXPORTER_OTLP_TIMEOUT"
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
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
