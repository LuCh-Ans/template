package positions

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Pool struct {
	queue          *Queue
	push           positionRequester
	workers        int
	requestTimeout time.Duration
	logger         *slog.Logger
	tracer         trace.Tracer
	metrics        *poolMetrics
	active         atomic.Int64
	started        atomic.Bool
}

func NewPool(queue *Queue, push positionRequester, workers int, requestTimeout time.Duration, logger *slog.Logger) (*Pool, error) {
	if workers <= 0 {
		return nil, fmt.Errorf("position workers must be positive, got %d", workers)
	}
	if requestTimeout <= 0 {
		return nil, fmt.Errorf("push request timeout must be positive, got %s", requestTimeout)
	}
	p := &Pool{
		queue: queue, push: push, workers: workers, requestTimeout: requestTimeout,
		logger: logger, tracer: otel.Tracer("trip-service/positions"),
	}
	metrics, err := newPoolMetrics(queue, &p.active)
	if err != nil {
		return nil, fmt.Errorf("initialize position worker metrics: %w", err)
	}
	p.metrics = metrics
	return p, nil
}

// Run запускается один раз и ждёт завершения всех воркеров.
// Close очереди завершает пул после обработки буфера. Отмена ctx прекращает
// ожидание и исходящие вызовы; она нужна при исчерпании SHUTDOWN_TIMEOUT.
// Контекст пула должен жить отдельно от контекста сканера, чтобы его остановка
// оставляла воркерам время на обработку уже поставленных задач.
func (p *Pool) Run(ctx context.Context) error {
	if !p.started.CompareAndSwap(false, true) {
		return fmt.Errorf("position worker pool already started")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var workers sync.WaitGroup
	for range p.workers {
		workers.Go(func() {
			for {
				job, ok := p.queue.next(ctx)
				if !ok {
					return
				}
				p.process(ctx, job)
			}
		})
	}
	workers.Wait()
	return ctx.Err()
}

// Close снимает callbacks метрик после завершения Run и финального сбора метрик.
// Также вызывается при ошибке сборки приложения, если Run ещё не был запущен.
func (p *Pool) Close() error {
	return p.metrics.registration.Unregister()
}

func (p *Pool) process(ctx context.Context, job *task) {
	defer p.queue.finish(job)
	requestID := uuid.NewString()
	opts := []trace.SpanStartOption{
		trace.WithNewRoot(),
		trace.WithAttributes(attribute.String("trip_id", job.trip.ID.String()), attribute.String("request_id", requestID)),
	}
	if job.scanContext.IsValid() {
		opts = append(opts, trace.WithLinks(trace.Link{SpanContext: job.scanContext}))
	}
	ctx, span := p.tracer.Start(ctx, "positions.request", opts...)
	defer span.End()
	ctx, cancel := context.WithTimeout(ctx, p.requestTimeout)
	defer cancel()

	p.active.Add(1)
	defer p.active.Add(-1)
	started := time.Now()
	err := p.push.RequestPosition(ctx, job.trip.ID, job.trip.DriverID, requestID)
	duration := time.Since(started)
	result := requestSuccess
	if err != nil {
		result = requestError
		span.RecordError(err)
		span.SetStatus(codes.Error, "request position")
		p.logger.ErrorContext(ctx, "request position failed", "trip_id", job.trip.ID, "request_id", requestID, "error", err)
	}
	p.metrics.record(ctx, result, duration)
}
