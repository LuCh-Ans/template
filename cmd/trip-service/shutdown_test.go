package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestShutdownDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- shutdown(ctx, &http.Server{ReadHeaderTimeout: time.Second}, func(context.Context) error {
				close(started)
				<-release // Имитируем Close, который не поддерживает отмену контекста.
				close(finished)
				return nil
			})
		}()
		<-started
		<-ctx.Done()
		synctest.Wait()
		select {
		case err := <-result:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("shutdown error = %v", err)
			}
		default:
			t.Error("shutdown did not return at its deadline")
		}
		close(release)
		<-finished
		synctest.Wait()
	})
}

func TestShutdownResult(t *testing.T) {
	failure := errors.New("export failed")
	for _, want := range []error{nil, failure} {
		t.Run("cleanup "+errorName(want), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			called := false
			err := shutdown(ctx, &http.Server{ReadHeaderTimeout: time.Second}, func(got context.Context) error {
				called = true
				if got != ctx {
					t.Error("shutdown context was replaced")
				}
				return want
			})
			if !called || !errors.Is(err, want) {
				t.Fatalf("called=%v error=%v, want %v", called, err, want)
			}
		})
	}
}

func errorName(err error) string {
	if err == nil {
		return "success"
	}
	return "failure"
}
