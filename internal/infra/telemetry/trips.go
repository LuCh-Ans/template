package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

type activeTripCounter interface {
	CountActive(ctx context.Context) (int64, error)
}

type TripMetrics struct {
	created      metric.Int64Counter
	completed    metric.Int64Counter
	registration metric.Registration
}

func NewTripMetrics(repo activeTripCounter) (*TripMetrics, error) {
	meter := otel.Meter("trip-service/trips")

	created, err := meter.Int64Counter(
		"tripgo.trips.created",
		metric.WithUnit("{trip}"),
		metric.WithDescription("Number of successfully created trips"),
	)
	if err != nil {
		return nil, fmt.Errorf("create trips created counter: %w", err)
	}

	completed, err := meter.Int64Counter(
		"tripgo.trips.completed",
		metric.WithUnit("{trip}"),
		metric.WithDescription("Number of successfully completed trips"),
	)
	if err != nil {
		return nil, fmt.Errorf("create trips completed counter: %w", err)
	}

	active, err := meter.Int64ObservableGauge(
		"tripgo.trips.active",
		metric.WithUnit("{trip}"),
		metric.WithDescription("Number of currently active trips"),
	)
	if err != nil {
		return nil, fmt.Errorf("create active trips gauge: %w", err)
	}

	registration, err := meter.RegisterCallback(
		func(ctx context.Context, observer metric.Observer) error {
			count, err := repo.CountActive(ctx)
			if err != nil {
				// Не выдаём ошибку БД за отсутствие активных поездок
				return fmt.Errorf("observe active trips: %w", err)
			}

			observer.ObserveInt64(active, count)
			return nil
		},
		active,
	)
	if err != nil {
		return nil, fmt.Errorf("register active trips callback: %w", err)
	}

	return &TripMetrics{
		created:      created,
		completed:    completed,
		registration: registration,
	}, nil
}

func (m *TripMetrics) TripCreated(ctx context.Context) {
	m.created.Add(ctx, 1)
}

func (m *TripMetrics) TripCompleted(ctx context.Context) {
	m.completed.Add(ctx, 1)
}

func (m *TripMetrics) Close() error {
	return m.registration.Unregister()
}
