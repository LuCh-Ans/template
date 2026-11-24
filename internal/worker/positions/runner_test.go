package positions

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/mock/gomock"
)

func TestRunnerStopsScannerBeforeDrainingPool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		scanner, pool, ticker := NewMockscannerRunner(ctrl), NewMockpoolRunner(ctrl), NewMockTicker(ctrl)
		scannerStopped, release := make(chan struct{}), make(chan struct{})
		ticker.EXPECT().Stop()
		scanner.EXPECT().Run(gomock.Any(), ticker).Do(func(ctx context.Context, tick Ticker) {
			<-ctx.Done()
			tick.Stop()
			close(scannerStopped)
		})
		pool.EXPECT().Run(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
			<-scannerStopped
			if ctx.Err() != nil {
				t.Error("pool cancelled before draining")
			}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		runner := NewRunner(scanner, pool, ticker)
		if err := runner.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- runner.Stop(t.Context()) }()
		<-scannerStopped
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("Stop returned before drain: %v", err)
		default:
		}
		close(release)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if err := runner.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := runner.Start(t.Context()); err == nil {
			t.Fatal("runner restarted")
		}
	})
}

func TestRunnerShutdownDeadlineCancelsPoolAndWaitsForExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		scanner, pool, ticker := NewMockscannerRunner(ctrl), NewMockpoolRunner(ctrl), NewMockTicker(ctrl)
		scanExited, poolExited := make(chan struct{}), make(chan struct{})
		scanner.EXPECT().Run(gomock.Any(), ticker).Do(func(ctx context.Context, _ Ticker) { <-ctx.Done(); close(scanExited) })
		pool.EXPECT().Run(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
			<-ctx.Done()
			close(poolExited)
			return ctx.Err()
		})
		runner := NewRunner(scanner, pool, ticker)
		if err := runner.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := runner.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error=%v", err)
		}
		select {
		case <-scanExited:
		default:
			t.Fatal("scanner left running")
		}
		select {
		case <-poolExited:
		default:
			t.Fatal("pool left running")
		}
	})
}

func TestRunnerReportsPoolError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		scanner, pool, ticker := NewMockscannerRunner(ctrl), NewMockpoolRunner(ctrl), NewMockTicker(ctrl)
		failure := errors.New("worker startup failed")
		scanner.EXPECT().Run(gomock.Any(), ticker).Do(func(ctx context.Context, _ Ticker) { <-ctx.Done() })
		pool.EXPECT().Run(gomock.Any()).Return(failure)
		runner := NewRunner(scanner, pool, ticker)
		if err := runner.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := runner.Start(t.Context()); err == nil {
			t.Fatal("runner started twice")
		}
		if err := runner.Stop(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestRunnerStopBeforeStart(t *testing.T) {
	ctrl := gomock.NewController(t)
	scanner, pool, ticker := NewMockscannerRunner(ctrl), NewMockpoolRunner(ctrl), NewMockTicker(ctrl)
	ticker.EXPECT().Stop()
	runner := NewRunner(scanner, pool, ticker)
	if err := runner.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runner.Start(t.Context()); err == nil {
		t.Fatal("stopped runner started")
	}
}

func TestRunnerRejectsCancelledStart(t *testing.T) {
	ctrl := gomock.NewController(t)
	scanner, pool, ticker := NewMockscannerRunner(ctrl), NewMockpoolRunner(ctrl), NewMockTicker(ctrl)
	ticker.EXPECT().Stop()
	runner := NewRunner(scanner, pool, ticker)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runner.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if err := runner.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}
