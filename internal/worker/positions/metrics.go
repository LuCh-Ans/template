package positions

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	requestSuccess = "success"
	requestError   = "error"
)

type poolMetrics struct {
	requests     metric.Int64Counter
	duration     metric.Float64Histogram
	registration metric.Registration
}

func newPoolMetrics(queue *Queue, active *atomic.Int64) (*poolMetrics, error) {
	meter := otel.Meter("trip-service/positions")
	requests, err := meter.Int64Counter("tripgo.position_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create position requests counter: %w", err)
	}
	duration, err := meter.Float64Histogram("tripgo.position_requests.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create position requests duration: %w", err)
	}
	size, err := meter.Int64ObservableGauge("tripgo.position_requests.queue.size", metric.WithUnit("{task}"))
	if err != nil {
		return nil, fmt.Errorf("create position queue size gauge: %w", err)
	}
	busy, err := meter.Int64ObservableGauge("tripgo.position_requests.workers.active", metric.WithUnit("{worker}"))
	if err != nil {
		return nil, fmt.Errorf("create active position workers gauge: %w", err)
	}
	registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		// Отменённые задачи занимают буфер до чтения, поэтому считаем физический
		// размер канала: именно он определяет, влезет ли следующая постановка.
		observer.ObserveInt64(size, int64(len(queue.tasks)))
		observer.ObserveInt64(busy, active.Load())
		return nil
	}, size, busy)
	if err != nil {
		return nil, fmt.Errorf("register position worker gauges: %w", err)
	}
	return &poolMetrics{requests: requests, duration: duration, registration: registration}, nil
}

func (m *poolMetrics) record(ctx context.Context, result string, duration time.Duration) {
	attrs := metric.WithAttributes(attribute.String("result", result))
	m.requests.Add(ctx, 1, attrs)
	m.duration.Record(ctx, duration.Seconds(), attrs)
}
