package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/LuCh-Ans/template/internal/http/middleware"
	"github.com/LuCh-Ans/template/internal/http/response"
)

func TestMiddleware(t *testing.T) {
	const tripPath = "/trips/one"
	const tripRoute = "/trips/{tripId}"
	for _, tc := range []struct {
		name, method, path, route string
		status                    int
		panicHandler              bool
	}{
		{"success", http.MethodGet, tripPath, tripRoute, 200, false},
		{"another id", http.MethodGet, "/trips/two", tripRoute, 200, false},
		{"invalid request", http.MethodGet, tripPath, tripRoute, 400, false},
		{"server error", http.MethodGet, tripPath, tripRoute, 503, false},
		{"panic", http.MethodGet, tripPath, tripRoute, 500, true},
		{"not found", http.MethodGet, "/unknown/secret-id", "unmatched", 404, false},
		{"method not allowed", http.MethodPost, tripPath, "unmatched", 405, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousTracer, previousMeter, previousPropagation := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator()
			exporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetTracerProvider(tp)
			otel.SetMeterProvider(mp)
			otel.SetTextMapPropagator(propagation.TraceContext{})
			defer func() {
				otel.SetTracerProvider(previousTracer)
				otel.SetMeterProvider(previousMeter)
				otel.SetTextMapPropagator(previousPropagation)
				if err := tp.Shutdown(t.Context()); err != nil {
					t.Error(err)
				}
				if err := mp.Shutdown(t.Context()); err != nil {
					t.Error(err)
				}
			}()

			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			router := chi.NewRouter()
			router.Get(tripRoute, func(w http.ResponseWriter, r *http.Request) {
				var during metricdata.ResourceMetrics
				if err := reader.Collect(r.Context(), &during); err != nil {
					t.Fatal(err)
				}
				assertActive(t, during, 1)
				if tc.panicHandler {
					panic("test panic")
				}
				w.WriteHeader(tc.status)
			})
			handler, err := middleware.Wrap(router, logger, response.New(logger))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
			if tc.panicHandler {
				var problem map[string]any
				if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if problem["code"] != "internal_error" || strings.Contains(recorder.Body.String(), "test panic") {
					t.Fatalf("unsafe panic response: %s", recorder.Body.String())
				}
			}
			count := 0
			decoder := json.NewDecoder(&output)
			for decoder.More() {
				var record map[string]any
				if err := decoder.Decode(&record); err != nil {
					t.Fatal(err)
				}
				if record["msg"] != "request handled" {
					continue
				}
				count++
				level := "INFO"
				if tc.status >= 500 {
					level = "ERROR"
				}
				if record["status"] != float64(tc.status) || record["route"] != tc.route || record["level"] != level {
					t.Fatalf("incorrect request log: %v", record)
				}
			}
			if count != 1 {
				t.Fatalf("request log count = %d", count)
			}
			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("span count = %d", len(spans))
			}
			span := spans[0]
			if span.Name != tc.method+" "+tc.route || span.Parent.SpanID().String() != "00f067aa0ba902b7" ||
				span.SpanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
				t.Fatalf("incorrect span name or parent: %+v", span)
			}
			attrs := attribute.NewSet(span.Attributes...)
			route, _ := attrs.Value("http.route")
			status, _ := attrs.Value("http.response.status_code")
			if route.AsString() != tc.route || status.AsInt64() != int64(tc.status) {
				t.Fatalf("incorrect span attributes: %v", span.Attributes)
			}
			if tc.status >= 400 {
				errorType, _ := attrs.Value("error.type")
				if span.Status.Code != codes.Error || errorType.AsString() != strconv.Itoa(tc.status) {
					t.Fatalf("missing span error: %+v", span)
				}
			}
			var after metricdata.ResourceMetrics
			if err := reader.Collect(t.Context(), &after); err != nil {
				t.Fatal(err)
			}
			assertActive(t, after, 0)
			assertDuration(t, after, tc.route, tc.status)
		})
	}
}

func assertActive(t *testing.T, data metricdata.ResourceMetrics, want int64) {
	t.Helper()
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "http.server.active_requests" {
				continue
			}
			sum := m.Data.(metricdata.Sum[int64])
			if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != want {
				t.Fatalf("active requests: %+v, want %d", sum, want)
			}
			return
		}
	}
	t.Fatal("active requests metric missing")
}

func assertDuration(t *testing.T, data metricdata.ResourceMetrics, route string, status int) {
	t.Helper()
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			histogram := m.Data.(metricdata.Histogram[float64])
			if len(histogram.DataPoints) != 1 {
				t.Fatalf("duration points: %+v", histogram)
			}
			point := histogram.DataPoints[0]
			gotRoute, _ := point.Attributes.Value("http.route")
			gotStatus, _ := point.Attributes.Value("http.response.status_code")
			if point.Count != 1 || gotRoute.AsString() != route || gotStatus.AsInt64() != int64(status) || m.Unit != "s" {
				t.Fatalf("incorrect duration metric: %+v", m)
			}
			return
		}
	}
	t.Fatal("duration metric missing")
}
