package telemetry_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/LuCh-Ans/template/internal/config"
	"github.com/LuCh-Ans/template/internal/infra/telemetry"
)

func TestSetupLogsSDKErrorsAsJSON(t *testing.T) {
	const serviceName = "test-service"
	previousTracer, previousMeter, previousPropagation := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator()
	// Устанавливаем конкретный обработчик до Setup: начальный handler SDK — делегат.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))
	previousHandler := otel.GetErrorHandler()
	defer func() {
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
		otel.SetTextMapPropagator(previousPropagation)
		otel.SetErrorHandler(previousHandler)
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var output bytes.Buffer
	logger := telemetry.NewLogger(&output, slog.LevelInfo, serviceName)
	shutdown, err := telemetry.Setup(t.Context(), config.TelemetryConfig{
		Endpoint: server.URL, ServiceName: serviceName, ServiceVersion: "test",
		ExportTimeout: time.Second, BatchTimeout: time.Second,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	// SDK вызывает тот же глобальный обработчик при ошибках фонового экспорта.
	otel.Handle(errors.New("collector unavailable"))
	if err := shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["level"] != "WARN" || record["service"] != serviceName || record["error"] != "collector unavailable" {
		t.Fatalf("log: %v", record)
	}
}
