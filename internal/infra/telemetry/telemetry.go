package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/LuCh-Ans/template/internal/config"
)

// Setup устанавливает общие providers и возвращает функцию их завершения.
func Setup(ctx context.Context, cfg config.TelemetryConfig, logger *slog.Logger) (func(context.Context) error, error) {
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("telemetry error", "error", err)
	}))
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse telemetry endpoint: %w", err)
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", cfg.ServiceVersion),
			attribute.String("service.namespace", "tripgo"),
			attribute.String("deployment.environment", "local"),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	traceExporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint.JoinPath("v1/traces").String()),
		otlptracehttp.WithTimeout(cfg.ExportTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	metricExporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(endpoint.JoinPath("v1/metrics").String()),
		otlpmetrichttp.WithTimeout(cfg.ExportTimeout),
	)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ExportTimeout)
		defer cancel()
		return nil, errors.Join(fmt.Errorf("create metric exporter: %w", err), traceExporter.Shutdown(cleanupCtx))
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter, sdktrace.WithBatchTimeout(cfg.BatchTimeout)),
	)
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	shutdown := func(ctx context.Context) error {
		return errors.Join(tracerProvider.Shutdown(ctx), meterProvider.Shutdown(ctx))
	}
	return shutdown, nil
}
