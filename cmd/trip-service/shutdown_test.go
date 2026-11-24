package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

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

func TestShutdownWaitsForHTTPBeforeBackgroundComponents(t *testing.T) {
	entered, release, handlerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		defer close(handlerDone)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	clientDone := make(chan error, 1)
	go func() {
		response, err := server.Client().Get(server.URL)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			if closeErr := response.Body.Close(); err == nil {
				err = closeErr
			}
		}
		clientDone <- err
	}()
	<-entered
	shutdownStarted, cleanupCalled := make(chan struct{}), make(chan struct{})
	server.Config.RegisterOnShutdown(func() { close(shutdownStarted) })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- shutdown(ctx, server.Config, func(context.Context) error {
			select {
			case <-handlerDone:
			default:
				t.Error("background shutdown preceded HTTP completion")
			}
			close(cleanupCalled)
			return nil
		})
	}()
	<-shutdownStarted
	select {
	case <-cleanupCalled:
		t.Error("cleanup called while HTTP request was active")
	default:
	}
	close(release)
	if err := <-clientDone; err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
