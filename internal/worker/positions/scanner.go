package positions

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type Scanner struct {
	repo       staleTripRepository
	queue      *Queue
	staleAfter time.Duration
	logger     *slog.Logger
	tracer     trace.Tracer
	checks     metric.Int64Counter
	requests   metric.Int64Counter
}

func NewScanner(repo staleTripRepository, queue *Queue, staleAfter time.Duration, logger *slog.Logger) (*Scanner, error) {
	if staleAfter <= 0 {
		return nil, fmt.Errorf("position stale interval must be positive, got %s", staleAfter)
	}
	meter := otel.Meter("trip-service/positions")
	checks, err := meter.Int64Counter("tripgo.stale_checks", metric.WithUnit("{check}"))
	if err != nil {
		return nil, fmt.Errorf("create stale checks counter: %w", err)
	}
	requests, err := meter.Int64Counter("tripgo.position_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create position requests counter: %w", err)
	}
	return &Scanner{
		repo: repo, queue: queue, staleAfter: staleAfter, logger: logger,
		tracer: otel.Tracer("trip-service/positions"), checks: checks, requests: requests,
	}, nil
}

// Run вызывается один раз в одной фоновой горутине с контекстом жизни сканера.
// Запрос и постановка синхронны: обходы не накладываются. Первый обход — по тику.
// Репозиторий ограничивает запрос своим DATABASE_QUERY_TIMEOUT.
// Run владеет тикером и закрывает очередь при выходе, оставляя буфер читателям.
func (s *Scanner) Run(ctx context.Context, ticker Ticker) {
	defer ticker.Stop()
	defer s.queue.Close()
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticker.C():
			if !ok || ctx.Err() != nil {
				return
			}
			s.scan(ctx)
		}
	}
}

func (s *Scanner) scan(ctx context.Context) {
	ctx, span := s.tracer.Start(ctx, "positions.scan")
	defer span.End()
	trips, err := s.repo.ListStaleTrips(ctx, s.staleAfter)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list stale trips")
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "list stale trips failed", "error", err)
		}
		return // Ошибка одного обхода не останавливает последующие тики.
	}
	for _, trip := range trips {
		if ctx.Err() != nil {
			return
		}
		result := s.queue.TryEnqueue(trip, span.SpanContext())
		attrs := metric.WithAttributes(attribute.String("result", string(result)))
		s.checks.Add(ctx, 1, attrs)
		if result == Dropped {
			s.requests.Add(ctx, 1, attrs)
			s.logger.WarnContext(ctx, "position request queue full", "trip_id", trip.ID)
		}
	}
}
