package positions

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

func readPoolMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range data.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			metrics[instrument.Name] = instrument
		}
	}
	return metrics
}

func assertPoolGauges(t *testing.T, metrics map[string]metricdata.Metrics, queued, active int64) {
	t.Helper()
	for name, want := range map[string]int64{
		"tripgo.position_requests.queue.size": queued, "tripgo.position_requests.workers.active": active,
	} {
		gauge, ok := metrics[name].Data.(metricdata.Gauge[int64])
		if !ok || len(gauge.DataPoints) != 1 {
			t.Fatalf("invalid gauge %s: %+v", name, metrics[name])
		}
		if point := gauge.DataPoints[0]; point.Value != want || point.Attributes.Len() != 0 {
			t.Fatalf("gauge %s = %+v, want %d without labels", name, point, want)
		}
	}
}

func TestPoolMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		queue := newTestQueue(t, 3)
		first, second, cancelled := testTrip(), testTrip(), testTrip()
		assertEnqueue(t, queue, first, Queued)
		assertEnqueue(t, queue, second, Queued)
		assertEnqueue(t, queue, cancelled, Queued)
		queue.Cancel(cancelled.ID)
		queue.Close()
		push := NewMockpositionRequester(gomock.NewController(t))
		release := make(chan struct{})
		push.EXPECT().RequestPosition(gomock.Any(), first.ID, first.DriverID, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ string) error {
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				timer := time.NewTimer(250 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		push.EXPECT().RequestPosition(gomock.Any(), second.ID, second.DriverID, gomock.Any()).Return(errors.New("unavailable"))
		pool := newTestPool(t, queue, push, 1, time.Minute, quietLogger())
		run := startTestPool(t, pool)
		synctest.Wait()
		assertPoolGauges(t, readPoolMetrics(t, reader), 2, 1)
		close(release)
		<-run.done
		if run.err != nil {
			t.Fatal(run.err)
		}
		metrics := readPoolMetrics(t, reader)
		assertPoolGauges(t, metrics, 0, 0)
		counts, ok := metrics["tripgo.position_requests"].Data.(metricdata.Sum[int64])
		if !ok || !counts.IsMonotonic || len(counts.DataPoints) != 2 {
			t.Fatalf("invalid requests counter: %+v", counts)
		}
		for _, point := range counts.DataPoints {
			result, present := point.Attributes.Value("result")
			if !present || point.Attributes.Len() != 1 || point.Value != 1 ||
				(result.AsString() != requestSuccess && result.AsString() != requestError) {
				t.Fatalf("invalid request result: %+v", point)
			}
		}
		durations, ok := metrics["tripgo.position_requests.duration"].Data.(metricdata.Histogram[float64])
		if !ok || metrics["tripgo.position_requests.duration"].Unit != "s" || len(durations.DataPoints) != 2 {
			t.Fatalf("invalid durations histogram: %+v", durations)
		}
		for _, point := range durations.DataPoints {
			result, present := point.Attributes.Value("result")
			want := 0.0
			if result.AsString() == requestSuccess {
				want = 0.25
			}
			if !present || point.Attributes.Len() != 1 || point.Count != 1 || point.Sum != want ||
				(result.AsString() != requestSuccess && result.AsString() != requestError) {
				t.Fatalf("invalid duration: %+v", point)
			}
		}
		if err := pool.Close(); err != nil {
			t.Fatal(err)
		}
		metrics = readPoolMetrics(t, reader)
		for _, name := range []string{"tripgo.position_requests.queue.size", "tripgo.position_requests.workers.active"} {
			if _, ok := metrics[name]; ok {
				t.Fatalf("%s still reported after unregister", name)
			}
		}
	})
}

func TestPoolSpanLinksToScanAndReleasesTaskContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		scanCtx, scan := provider.Tracer("test").Start(t.Context(), "positions.scan")
		queue := newTestQueue(t, 1)
		trip := testTrip()
		if result := queue.TryEnqueue(trip, scan.SpanContext()); result != Queued {
			t.Fatal(result)
		}
		scan.End() // Обработка переживает спан проверки.
		queue.Close()
		push := NewMockpositionRequester(gomock.NewController(t))
		var callContext context.Context
		var callSpan trace.SpanContext
		var requestID string
		push.EXPECT().RequestPosition(gomock.Any(), trip.ID, trip.DriverID, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, id string) error {
				callContext, callSpan, requestID = ctx, trace.SpanContextFromContext(ctx), id
				return errors.New("push rejected request")
			})
		pool := newTestPool(t, queue, push, 1, time.Minute, quietLogger())
		if err := pool.Run(scanCtx); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(callContext.Err(), context.Canceled) {
			t.Fatal("task timer was not released after gateway returned")
		}
		spans := recorder.Ended()
		if len(spans) != 2 {
			t.Fatalf("spans = %+v", spans)
		}
		worker := spans[1]
		if worker.Name() != "positions.request" || worker.Parent().IsValid() ||
			worker.SpanContext().TraceID() == scan.SpanContext().TraceID() || !callSpan.Equal(worker.SpanContext()) {
			t.Fatalf("worker did not start an independent trace: %+v", worker)
		}
		if links := worker.Links(); len(links) != 1 || !links[0].SpanContext.Equal(scan.SpanContext()) {
			t.Fatalf("worker links = %+v", links)
		}
		if worker.Status().Code != codes.Error || len(worker.Events()) != 1 {
			t.Fatal("push error was not recorded on worker span")
		}
		attrs := map[string]string{}
		for _, attr := range worker.Attributes() {
			attrs[string(attr.Key)] = attr.Value.AsString()
		}
		if attrs["trip_id"] != trip.ID.String() || attrs["request_id"] != requestID || requestID == "" {
			t.Fatalf("worker attributes = %v", attrs)
		}
	})
}
