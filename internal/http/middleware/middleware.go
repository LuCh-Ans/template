// Package middleware instruments HTTP requests in the course-defined order.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/LuCh-Ans/template/internal/http/response"
)

type stateKey struct{}

type requestState struct {
	route     string
	method    string
	logger    *slog.Logger
	responses *response.Writer
}

// recoverRequest is also deferred at the router boundary so recovery completes
// before otelhttp ends the span and records the response status.
func (s *requestState) recoverRequest(w http.ResponseWriter, r *http.Request) {
	value := recover()
	if value == nil {
		return
	}
	if value == http.ErrAbortHandler {
		panic(value)
	}
	span := trace.SpanFromContext(r.Context())
	span.SetStatus(codes.Error, "handler panic")
	span.SetAttributes(attribute.String("error.type", "panic"))
	s.logger.ErrorContext(r.Context(), "panic in handler",
		"panic", value, "stack", string(debug.Stack()), "method", s.method, "route", s.route)
	// A committed response cannot be replaced with a JSON error.
	if writer, ok := w.(chimiddleware.WrapResponseWriter); ok && writer.Status() != 0 {
		return
	}
	s.responses.Problem(w, r, http.StatusInternalServerError, "internal_error", "Internal Server Error", "Internal server error")
}

// Wrap builds Recover → Tracing → Logging → Metrics → router.
func Wrap(router *chi.Mux, logger *slog.Logger, responses *response.Writer) (http.Handler, error) {
	active, err := otel.Meter("trip-service/http").Int64UpDownCounter(
		"http.server.active_requests", metric.WithUnit("{request}"),
		metric.WithDescription("Number of HTTP requests currently being handled"),
	)
	if err != nil {
		return nil, fmt.Errorf("create active request metric: %w", err)
	}

	metrics := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.Context().Value(stateKey{}).(*requestState)
		attrs := metric.WithAttributes(
			attribute.String("http.request.method", state.method),
			attribute.String("http.route", state.route),
		)
		active.Add(r.Context(), 1, attrs)
		defer active.Add(r.Context(), -1, attrs)
		defer state.recoverRequest(w, r)
		router.ServeHTTP(w, r)
	})

	logging := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.Context().Value(stateKey{}).(*requestState)
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("http.route", state.route))
		labeler, _ := otelhttp.LabelerFromContext(r.Context())
		labeler.Add(attribute.String("http.route", state.route))
		writer := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		started := time.Now()
		metrics.ServeHTTP(writer, r)
		status := writer.Status()
		if status == 0 {
			status = http.StatusOK
		}
		if status >= http.StatusBadRequest {
			span.SetStatus(codes.Error, http.StatusText(status))
			span.SetAttributes(attribute.String("error.type", strconv.Itoa(status)))
		}
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		logger.Log(r.Context(), level, "request handled",
			"method", state.method, "route", state.route, "status", status,
			"duration_ms", float64(time.Since(started))/float64(time.Millisecond),
		)
	})

	tracing := otelhttp.NewHandler(logging, "http request",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			state := r.Context().Value(stateKey{}).(*requestState)
			return state.method + " " + state.route
		}),
	)
	recoverer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := router.Find(chi.NewRouteContext(), r.Method, r.URL.Path)
		if route == "" {
			route = "unmatched"
		}
		state := &requestState{route: route, method: requestMethod(r.Method), logger: logger, responses: responses}
		r = r.WithContext(context.WithValue(r.Context(), stateKey{}, state))
		defer state.recoverRequest(w, r)
		tracing.ServeHTTP(w, r)
	})
	return recoverer, nil
}

func requestMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return method
	default:
		return "_OTHER"
	}
}
