package telemetry_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/LuCh-Ans/template/internal/infra/telemetry"
)

func TestLoggerCorrelation(t *testing.T) {
	var output bytes.Buffer
	logger := telemetry.NewLogger(&output, slog.LevelInfo, "trip-service").With("component", "test")
	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3},
		SpanID:  trace.SpanID{4, 5, 6},
	})
	ctx := trace.ContextWithSpanContext(t.Context(), span)
	logger.DebugContext(ctx, "hidden")
	if output.Len() != 0 {
		t.Fatal("debug message passed INFO threshold")
	}
	logger.InfoContext(ctx, "trip created", "trip_id", "trip-1")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"service": "trip-service", "component": "test", "msg": "trip created",
		"level": "INFO", "trace_id": span.TraceID().String(), "span_id": span.SpanID().String(), "trip_id": "trip-1",
	} {
		if record[key] != want {
			t.Errorf("%s = %v, want %s", key, record[key], want)
		}
	}
	output.Reset()
	logger.InfoContext(t.Context(), "service started")
	record = nil
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"trace_id", "span_id"} {
		if _, ok := record[key]; ok {
			t.Errorf("%s present outside span", key)
		}
	}
}

func TestLoggerWithGroup(t *testing.T) {
	var output bytes.Buffer
	logger := telemetry.NewLogger(&output, slog.LevelInfo, "trip-service").WithGroup("operation").With("name", "create")
	logger.InfoContext(t.Context(), "test", "attempt", 1)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	group, ok := record["operation"].(map[string]any)
	if !ok || group["name"] != "create" || group["attempt"] != float64(1) || record["service"] != "trip-service" {
		t.Fatalf("unexpected grouped log: %s", output.String())
	}
}
