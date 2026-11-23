package positions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type scannerHarness struct {
	repo   *MockstaleTripRepository
	queue  *Queue
	ticks  chan time.Time
	ready  chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
	logs   bytes.Buffer
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for scanner")
	}
}

func newScannerHarness(t *testing.T, size int) *scannerHarness {
	t.Helper()
	ctrl := gomock.NewController(t)
	h := &scannerHarness{
		repo: NewMockstaleTripRepository(ctrl), queue: newTestQueue(t, size),
		ticks: make(chan time.Time, 1), ready: make(chan struct{}, 1), done: make(chan struct{}),
	}
	ticker := NewMockTicker(ctrl)
	ticker.EXPECT().C().DoAndReturn(func() <-chan time.Time {
		h.ready <- struct{}{} // Возврат к select означает, что предыдущий обход закончен.
		return h.ticks
	}).AnyTimes()
	ticker.EXPECT().Stop()
	scanner, err := NewScanner(h.repo, h.queue, time.Minute, slog.New(slog.NewJSONHandler(&h.logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	h.cancel = cancel
	go func() {
		defer close(h.done)
		scanner.Run(ctx, ticker)
	}()
	t.Cleanup(func() {
		cancel()
		waitSignal(t, h.done)
	})
	waitSignal(t, h.ready)
	return h
}

func (h *scannerHarness) tick(t *testing.T) {
	t.Helper()
	h.ticks <- time.Time{}
	waitSignal(t, h.ready)
}

func collectCounters(t *testing.T, reader *sdkmetric.ManualReader) map[string]map[string]int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]map[string]int64)
	for _, scope := range data.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			value, ok := instrument.Data.(metricdata.Sum[int64])
			if !ok || !value.IsMonotonic {
				t.Fatalf("unexpected metric: %+v", instrument)
			}
			got[instrument.Name] = make(map[string]int64)
			for _, point := range value.DataPoints {
				result, ok := point.Attributes.Value("result")
				if !ok || point.Attributes.Len() != 1 {
					t.Fatalf("unexpected attributes: %v", point.Attributes)
				}
				got[instrument.Name][result.AsString()] = point.Value
			}
		}
	}
	return got
}

func TestScannerResultsOverflowAndNextTick(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	h := newScannerHarness(t, 1)
	first, second, third := testTrip(), testTrip(), testTrip()
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).
		Return([]domain.StaleTrip{first, second, third}, nil).Times(2)
	h.tick(t) // Три результата на полной очереди: queued, dropped, dropped.
	h.tick(t) // duplicate, dropped, dropped: сканер продолжает проверять весь список.
	job, ok := h.queue.next(t.Context())
	if !ok || job.trip != first {
		t.Fatalf("first task = %+v, %v", job, ok)
	}
	h.queue.finish(job)
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).Return([]domain.StaleTrip{second}, nil)
	h.tick(t) // Ранее дропнутая поездка снова ставится на следующем тике.
	counters := collectCounters(t, reader)
	for result, want := range map[string]int64{"queued": 2, "duplicate": 1, "dropped": 4} {
		if got := counters["tripgo.stale_checks"][result]; got != want {
			t.Errorf("stale checks %s = %d, want %d", result, got, want)
		}
	}
	if got := counters["tripgo.position_requests"]["dropped"]; got != 4 {
		t.Errorf("dropped requests = %d, want 4", got)
	}
	decoder := json.NewDecoder(bytes.NewReader(h.logs.Bytes()))
	for range 4 {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil || record["level"] != "WARN" {
			t.Fatalf("overflow log = %v, error = %v", record, err)
		}
	}
	var extra map[string]any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected additional log: %v, %v", extra, err)
	}
}

func TestScannerContinuesAfterRepositoryError(t *testing.T) {
	h := newScannerHarness(t, 1)
	trip := testTrip()
	gomock.InOrder(
		h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).Return(nil, errors.New("database unavailable")),
		h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).Return([]domain.StaleTrip{trip}, nil),
	)
	h.tick(t)
	h.tick(t)
	job, ok := h.queue.next(t.Context())
	if !ok || job.trip != trip {
		t.Fatalf("task after failed scan = %+v, %v", job, ok)
	}
	h.queue.finish(job)
	if bytes.Count(h.logs.Bytes(), []byte(`"level":"ERROR"`)) != 1 {
		t.Fatalf("unexpected error logs: %s", h.logs.String())
	}
}

func TestScannerDoesNotOverlapScans(t *testing.T) {
	h := newScannerHarness(t, 2)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).
		DoAndReturn(func(ctx context.Context, _ time.Duration) ([]domain.StaleTrip, error) {
			if calls.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, nil
		}).Times(2)
	h.ticks <- time.Time{}
	waitSignal(t, entered)
	h.ticks <- time.Time{} // Следующий тик ждёт, пока запрос первого обхода заблокирован.
	select {
	case <-h.ready:
		t.Fatal("scanner returned to ticker before repository call finished")
	default:
	}
	if calls.Load() != 1 || len(h.ticks) != 1 {
		t.Fatal("scan overlapped the previous call")
	}
	close(release)
	waitSignal(t, h.ready)
	waitSignal(t, h.ready)
	if calls.Load() != 2 {
		t.Fatalf("scans = %d, want 2", calls.Load())
	}
}

func TestScannerStopsAndKeepsQueuedTasksForDrain(t *testing.T) {
	h := newScannerHarness(t, 1)
	trip := testTrip()
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).Return([]domain.StaleTrip{trip}, nil)
	h.tick(t)
	h.cancel()
	waitSignal(t, h.done)
	job, ok := h.queue.next(t.Context())
	if !ok || job.trip != trip {
		t.Fatalf("task lost on shutdown: %+v, %v", job, ok)
	}
	h.queue.finish(job)
	if _, ok := h.queue.next(t.Context()); ok {
		t.Fatal("queue not closed on scanner exit")
	}
}

func TestScannerCancelsRepositoryCall(t *testing.T) {
	h := newScannerHarness(t, 1)
	entered := make(chan struct{})
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).
		DoAndReturn(func(ctx context.Context, _ time.Duration) ([]domain.StaleTrip, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		})
	h.ticks <- time.Time{}
	waitSignal(t, entered)
	h.cancel()
	waitSignal(t, h.done)
	if h.logs.Len() != 0 {
		t.Fatalf("normal cancellation logged as failure: %s", h.logs.String())
	}
}

func TestScannerPreservesScanSpanContext(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	h := newScannerHarness(t, 1)
	trip := testTrip()
	var scan trace.SpanContext
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).
		DoAndReturn(func(ctx context.Context, _ time.Duration) ([]domain.StaleTrip, error) {
			scan = trace.SpanContextFromContext(ctx)
			return []domain.StaleTrip{trip}, nil
		})
	h.tick(t)
	job, ok := h.queue.next(t.Context())
	if !ok || !scan.IsValid() || !job.scanContext.Equal(scan) {
		t.Fatalf("scan context not preserved: %+v, %v", job, ok)
	}
	h.queue.finish(job)
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "positions.scan" {
		t.Fatalf("scan spans = %+v", spans)
	}
}

func TestScannerClosedTicker(t *testing.T) {
	h := newScannerHarness(t, 1)
	close(h.ticks)
	waitSignal(t, h.done)
}

func TestScannerRejectsInvalidInterval(t *testing.T) {
	for _, staleAfter := range []time.Duration{0, -time.Second} {
		if _, err := NewScanner(nil, newTestQueue(t, 1), staleAfter, slog.New(slog.DiscardHandler)); err == nil {
			t.Fatalf("stale interval %s accepted", staleAfter)
		}
	}
}

func TestSystemTicker(t *testing.T) {
	ticker := NewTicker(time.Hour)
	if ticker.C() == nil {
		t.Fatal("system ticker has no channel")
	}
	ticker.Stop()
	ticker.Stop()
}
