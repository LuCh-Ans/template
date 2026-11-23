package positions

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/goleak"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func newTestQueue(t *testing.T, size int) *Queue {
	t.Helper()
	q, err := NewQueue(size)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func testTrip() domain.StaleTrip {
	return domain.StaleTrip{
		ID: uuid.New(), DriverID: uuid.New(),
		LastActivity: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
	}
}

func assertEnqueue(t *testing.T, q *Queue, trip domain.StaleTrip, want EnqueueResult) {
	t.Helper()
	if got := q.TryEnqueue(trip, trace.SpanContext{}); got != want {
		t.Fatalf("enqueue = %s, want %s", got, want)
	}
}

func TestQueueFIFOAndInFlightDeduplication(t *testing.T) {
	q := newTestQueue(t, 2)
	defer q.Close()
	first, second := testTrip(), testTrip()
	scan := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	if got := q.TryEnqueue(first, scan); got != Queued {
		t.Fatal(got)
	}
	assertEnqueue(t, q, second, Queued)
	assertEnqueue(t, q, first, Duplicate) // Duplicate имеет приоритет над переполнением.
	job, ok := q.next(t.Context())
	if !ok || job.trip != first || !job.scanContext.Equal(scan) {
		t.Fatalf("first task = %+v, %v", job, ok)
	}
	q.Cancel(first.ID) // Начавшийся запрос остаётся под дедупликацией.
	assertEnqueue(t, q, first, Duplicate)
	q.finish(job)
	assertEnqueue(t, q, first, Queued) // Повтор разрешён после обработки, на новом тике.
	q.Close()
	for _, want := range []domain.StaleTrip{second, first} {
		job, ok = q.next(t.Context())
		if !ok || job.trip != want {
			t.Fatalf("FIFO task = %+v, %v; want %+v", job, ok, want)
		}
		q.finish(job)
	}
	if _, ok := q.next(t.Context()); ok {
		t.Fatal("closed, drained queue returned a task")
	}
	assertEnqueue(t, q, first, Dropped)
}

func TestQueueCancellationDoesNotAffectReplacement(t *testing.T) {
	q := newTestQueue(t, 2)
	trip := testTrip()
	assertEnqueue(t, q, trip, Queued)
	old := q.pending[trip.ID]
	q.Cancel(trip.ID)
	q.Cancel(trip.ID)
	q.Cancel(uuid.New())
	replacement := trip
	replacement.DriverID = uuid.New()
	assertEnqueue(t, q, replacement, Queued)
	q.finish(old) // Старая отменённая задача не может удалить новую запись.
	assertEnqueue(t, q, replacement, Duplicate)
	q.Close()
	job, ok := q.next(t.Context())
	if !ok || job == old || job.trip != replacement {
		t.Fatalf("replacement task = %+v, %v", job, ok)
	}
	q.finish(job)
	if _, ok := q.next(t.Context()); ok {
		t.Fatal("cancelled task was returned")
	}
}

func TestQueueOverflowDoesNotReserveTrip(t *testing.T) {
	q := newTestQueue(t, 1)
	defer q.Close()
	first, dropped := testTrip(), testTrip()
	assertEnqueue(t, q, first, Queued)
	assertEnqueue(t, q, dropped, Dropped)
	job, ok := q.next(t.Context())
	if !ok {
		t.Fatal("missing queued task")
	}
	q.finish(job)
	assertEnqueue(t, q, dropped, Queued)
}

func TestQueueCanceledTasksStillOccupyChannel(t *testing.T) {
	q := newTestQueue(t, 1)
	trip := testTrip()
	assertEnqueue(t, q, trip, Queued)
	q.Cancel(trip.ID)
	assertEnqueue(t, q, trip, Dropped)
	q.Close()
	if _, ok := q.next(t.Context()); ok {
		t.Fatal("cancelled task was returned")
	}
}

func TestQueueCanceledRead(t *testing.T) {
	q := newTestQueue(t, 1)
	defer q.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, ok := q.next(ctx); ok {
		t.Fatal("read with cancelled context returned a task")
	}
}

func TestQueueConcurrentDeduplication(t *testing.T) {
	q := newTestQueue(t, 32)
	defer q.Close()
	trip := testTrip()
	var queued, duplicates atomic.Int64
	var producers sync.WaitGroup
	for range 32 {
		producers.Go(func() {
			switch q.TryEnqueue(trip, trace.SpanContext{}) {
			case Queued:
				queued.Add(1)
			case Duplicate:
				duplicates.Add(1)
			default:
				t.Error("unexpected drop")
			}
		})
	}
	producers.Wait()
	if queued.Load() != 1 || duplicates.Load() != 31 || len(q.tasks) != 1 {
		t.Fatalf("queued=%d, duplicates=%d, buffered=%d", queued.Load(), duplicates.Load(), len(q.tasks))
	}
}

func TestQueueConcurrentLifecycle(t *testing.T) {
	q := newTestQueue(t, 4)
	trips := []domain.StaleTrip{testTrip(), testTrip(), testTrip()}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var consumer sync.WaitGroup
	consumer.Go(func() {
		for {
			job, ok := q.next(ctx)
			if !ok {
				return
			}
			q.finish(job)
		}
	})
	var producers sync.WaitGroup
	for range 8 {
		producers.Go(func() {
			for i := range 100 {
				trip := trips[i%len(trips)]
				q.TryEnqueue(trip, trace.SpanContext{})
				q.Cancel(trip.ID)
			}
		})
	}
	producers.Go(q.Close) // Close может встретиться с отправкой и отменой.
	producers.Wait()
	q.Close()
	consumer.Wait()
	if ctx.Err() != nil {
		t.Fatal("queue did not drain")
	}
	if len(q.pending) != 0 {
		t.Fatalf("pending after drain = %d", len(q.pending))
	}
}

func TestQueueRejectsInvalidSize(t *testing.T) {
	for _, size := range []int{0, -1} {
		if _, err := NewQueue(size); err == nil {
			t.Fatalf("size %d accepted", size)
		}
	}
}
