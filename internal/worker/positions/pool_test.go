package positions

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type poolRun struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error // читается только после закрытия done
}

func newTestPool(t *testing.T, queue *Queue, push positionRequester, workers int, timeout time.Duration, logger *slog.Logger) *Pool {
	t.Helper()
	pool, err := NewPool(queue, push, workers, timeout, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	})
	return pool
}

func startTestPool(t *testing.T, pool *Pool) *poolRun {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	run := &poolRun{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(run.done)
		run.err = pool.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-run.done
	})
	return run
}

func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestPoolLimitsParallelismAndDrainsQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const workers, total = 3, 10
		queue := newTestQueue(t, total)
		push := NewMockpositionRequester(gomock.NewController(t))
		release := make(chan struct{})
		var active, maximum, calls atomic.Int64
		for range total {
			trip := testTrip()
			assertEnqueue(t, queue, trip, Queued)
			push.EXPECT().RequestPosition(gomock.Any(), trip.ID, trip.DriverID, gomock.Not("")).
				DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, requestID string) error {
					if _, err := uuid.Parse(requestID); err != nil {
						t.Errorf("invalid request ID: %q", requestID)
					}
					current := active.Add(1)
					defer active.Add(-1)
					for previous := maximum.Load(); current > previous; previous = maximum.Load() {
						if maximum.CompareAndSwap(previous, current) {
							break
						}
					}
					calls.Add(1)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
		}
		queue.Close()
		pool := newTestPool(t, queue, push, workers, time.Minute, quietLogger())
		run := startTestPool(t, pool)
		synctest.Wait()
		if active.Load() != workers || calls.Load() != workers || len(queue.tasks) != total-workers {
			t.Fatalf("before release: active=%d, calls=%d, buffered=%d", active.Load(), calls.Load(), len(queue.tasks))
		}
		close(release)
		<-run.done
		if run.err != nil || calls.Load() != total || maximum.Load() != workers || active.Load() != 0 {
			t.Fatalf("after drain: error=%v, calls=%d, max=%d, active=%d", run.err, calls.Load(), maximum.Load(), active.Load())
		}
		if len(queue.pending) != 0 || len(queue.tasks) != 0 {
			t.Fatal("queue not drained")
		}
	})
}

func TestPoolCancellationSkipsOldTaskAndKeepsReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newTestQueue(t, 3)
		first, trip := testTrip(), testTrip()
		assertEnqueue(t, queue, first, Queued)
		assertEnqueue(t, queue, trip, Queued)
		push := NewMockpositionRequester(gomock.NewController(t))
		release := make(chan struct{})
		push.EXPECT().RequestPosition(gomock.Any(), first.ID, first.DriverID, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ string) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		replacement := trip
		replacement.DriverID = uuid.New()
		push.EXPECT().RequestPosition(gomock.Any(), replacement.ID, replacement.DriverID, gomock.Any()).Return(nil)
		pool := newTestPool(t, queue, push, 1, time.Minute, quietLogger())
		run := startTestPool(t, pool)
		synctest.Wait()
		queue.Cancel(trip.ID)
		assertEnqueue(t, queue, replacement, Queued)
		assertEnqueue(t, queue, replacement, Duplicate)
		queue.Close()
		close(release)
		<-run.done
		if run.err != nil || len(queue.pending) != 0 {
			t.Fatalf("error=%v, pending=%d", run.err, len(queue.pending))
		}
	})
}

func TestPoolDoesNotSendCancelledTask(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newTestQueue(t, 1)
		trip := testTrip()
		assertEnqueue(t, queue, trip, Queued)
		queue.Cancel(trip.ID)
		queue.Close()
		push := NewMockpositionRequester(gomock.NewController(t)) // Ни одного вызова.
		pool := newTestPool(t, queue, push, 2, time.Minute, quietLogger())
		if err := pool.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPoolContinuesAfterPushErrorWithoutRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newTestQueue(t, 2)
		first, second := testTrip(), testTrip()
		assertEnqueue(t, queue, first, Queued)
		assertEnqueue(t, queue, second, Queued)
		queue.Close()
		push := NewMockpositionRequester(gomock.NewController(t))
		gomock.InOrder(
			push.EXPECT().RequestPosition(gomock.Any(), first.ID, first.DriverID, gomock.Any()).Return(errors.New("push unavailable")),
			push.EXPECT().RequestPosition(gomock.Any(), second.ID, second.DriverID, gomock.Any()).Return(nil),
		)
		var logs bytes.Buffer
		pool := newTestPool(t, queue, push, 1, time.Minute, slog.New(slog.NewJSONHandler(&logs, nil)))
		if err := pool.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
		if bytes.Count(logs.Bytes(), []byte(`"level":"ERROR"`)) != 1 ||
			!bytes.Contains(logs.Bytes(), []byte(`"request_id":`)) {
			t.Fatalf("push failure log = %s", logs.String())
		}
		if len(queue.pending) != 0 {
			t.Fatal("failed task still prevents a future scan")
		}
	})
}

func TestPoolDeduplicatesInFlightAndRepeatsOnlyOnNextTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newScannerHarness(t, 1)
		trip := testTrip()
		h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).Return([]domain.StaleTrip{trip}, nil).Times(3)
		push := NewMockpositionRequester(gomock.NewController(t))
		release := make(chan struct{})
		var requests []string
		push.EXPECT().RequestPosition(gomock.Any(), trip.ID, trip.DriverID, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, requestID string) error {
				requests = append(requests, requestID)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}).Times(2)
		pool := newTestPool(t, h.queue, push, 2, time.Minute, quietLogger())
		run := startTestPool(t, pool)
		h.tick(t)
		synctest.Wait()
		h.tick(t) // Первая задача ещё выполняется: дубликат не должен дойти до Push.
		synctest.Wait()
		if len(requests) != 1 || len(h.queue.tasks) != 0 {
			t.Fatal("duplicate sent while first request was in flight")
		}
		close(release)
		synctest.Wait()
		if len(requests) != 1 {
			t.Fatal("pool retried a finished task without another tick")
		}
		h.tick(t)
		synctest.Wait()
		if len(requests) != 2 || requests[0] == requests[1] {
			t.Fatalf("new tick did not create a separate request: %v", requests)
		}
		h.cancel()
		<-h.done
		<-run.done
		if run.err != nil {
			t.Fatal(run.err)
		}
	})
}

func TestPoolTimeoutDoesNotStopWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newTestQueue(t, 2)
		first, second := testTrip(), testTrip()
		assertEnqueue(t, queue, first, Queued)
		assertEnqueue(t, queue, second, Queued)
		queue.Close()
		push := NewMockpositionRequester(gomock.NewController(t))
		gomock.InOrder(
			push.EXPECT().RequestPosition(gomock.Any(), first.ID, first.DriverID, gomock.Any()).
				DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ string) error {
					start := time.Now()
					deadline, ok := ctx.Deadline()
					if !ok || deadline.Sub(start) != time.Second {
						t.Error("task timeout was not applied")
					}
					<-ctx.Done() // Время testing/synctest продвигается без реального ожидания.
					if !errors.Is(ctx.Err(), context.DeadlineExceeded) || time.Since(start) != time.Second {
						t.Error("task did not expire after its configured timeout")
					}
					return ctx.Err()
				}),
			push.EXPECT().RequestPosition(gomock.Any(), second.ID, second.DriverID, gomock.Any()).Return(nil),
		)
		pool := newTestPool(t, queue, push, 1, time.Second, quietLogger())
		if err := pool.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPoolCancellationStopsActiveAndIdleWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newTestQueue(t, 1)
		trip := testTrip()
		assertEnqueue(t, queue, trip, Queued)
		push := NewMockpositionRequester(gomock.NewController(t))
		push.EXPECT().RequestPosition(gomock.Any(), trip.ID, trip.DriverID, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ string) error {
				<-ctx.Done()
				return ctx.Err()
			})
		pool := newTestPool(t, queue, push, 3, time.Hour, quietLogger())
		run := startTestPool(t, pool)
		synctest.Wait() // Один воркер в Push, два ждут очередь.
		assertEnqueue(t, queue, trip, Duplicate)
		run.cancel()
		<-run.done
		if !errors.Is(run.err, context.Canceled) || pool.active.Load() != 0 {
			t.Fatalf("error=%v, active=%d", run.err, pool.active.Load())
		}
		assertEnqueue(t, queue, trip, Queued) // Обработанная задача удалена и при отмене.
		queue.Close()
	})
}

func TestPoolShutdownBudgetCancelsPushAndLeavesUnstartedTasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newTestQueue(t, 2)
		first, second := testTrip(), testTrip()
		assertEnqueue(t, queue, first, Queued)
		assertEnqueue(t, queue, second, Queued)
		queue.Close()
		push := NewMockpositionRequester(gomock.NewController(t))
		push.EXPECT().RequestPosition(gomock.Any(), first.ID, first.DriverID, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ string) error {
				<-ctx.Done()
				return ctx.Err()
			})
		pool := newTestPool(t, queue, push, 1, time.Hour, quietLogger())
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		start := time.Now()
		if err := pool.Run(ctx); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 2*time.Second {
			t.Fatalf("shutdown error=%v, elapsed=%s", err, time.Since(start))
		}
		if len(queue.tasks) != 1 || len(queue.pending) != 1 {
			t.Fatal("worker started another task after shutdown deadline")
		}
	})
}

func TestPoolDoesNotStartTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newTestQueue(t, 1)
		push := NewMockpositionRequester(gomock.NewController(t))
		pool := newTestPool(t, queue, push, 1, time.Minute, quietLogger())
		run := startTestPool(t, pool)
		synctest.Wait()
		if err := pool.Run(t.Context()); err == nil {
			t.Fatal("pool started more workers on repeated Run")
		}
		queue.Close()
		<-run.done
		if run.err != nil {
			t.Fatal(run.err)
		}
	})
}

func TestPoolAlreadyCancelledContext(t *testing.T) {
	queue := newTestQueue(t, 1)
	defer queue.Close()
	push := NewMockpositionRequester(gomock.NewController(t))
	pool := newTestPool(t, queue, push, 1, time.Second, quietLogger())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := pool.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want cancelled", err)
	}
}

func TestPoolRejectsInvalidConfig(t *testing.T) {
	for _, workers := range []int{0, -1} {
		if _, err := NewPool(nil, nil, workers, time.Second, quietLogger()); err == nil {
			t.Fatalf("invalid worker count %d accepted", workers)
		}
	}
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, err := NewPool(nil, nil, 1, timeout, quietLogger()); err == nil {
			t.Fatalf("invalid timeout %s accepted", timeout)
		}
	}
}

func TestPoolDrainsAfterScannerStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newScannerHarness(t, 3)
		first, second, third := testTrip(), testTrip(), testTrip()
		h.repo.EXPECT().ListStaleTrips(gomock.Any(), time.Minute).
			Return([]domain.StaleTrip{first, second, third}, nil)
		push := NewMockpositionRequester(gomock.NewController(t))
		release := make(chan struct{})
		push.EXPECT().RequestPosition(gomock.Any(), first.ID, first.DriverID, gomock.Any()).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ string) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					t.Error("stopping the scanner cancelled the active push")
					return ctx.Err()
				}
			})
		push.EXPECT().RequestPosition(gomock.Any(), second.ID, second.DriverID, gomock.Any()).Return(nil)
		push.EXPECT().RequestPosition(gomock.Any(), third.ID, third.DriverID, gomock.Any()).Return(nil)
		pool := newTestPool(t, h.queue, push, 1, time.Minute, quietLogger())
		run := startTestPool(t, pool)
		h.tick(t)
		synctest.Wait()
		h.cancel()
		<-h.done
		synctest.Wait()
		if len(h.queue.tasks) != 2 || pool.active.Load() != 1 {
			t.Fatal("scanner shutdown lost work")
		}
		select {
		case <-run.done:
			t.Fatal("pool stopped before draining")
		default:
		}
		close(release)
		<-run.done
		if run.err != nil || len(h.queue.pending) != 0 {
			t.Fatalf("drain error=%v, pending=%d", run.err, len(h.queue.pending))
		}
	})
}
