package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestQueryTelemetry(t *testing.T) {
	const (
		collection      = "trips"
		selectOperation = "SELECT"
		insertOperation = "INSERT"
		selectTrips     = "SELECT id FROM trips"
	)
	for _, tc := range []struct {
		name, sql, operation, table, errorType string
		err                                    error
		sampled                                bool
	}{
		{"select", "SELECT id FROM trips WHERE id = $1", selectOperation, collection, "", nil, true},
		{"history", "INSERT INTO trip_status_history (trip_id) VALUES ($1)", insertOperation, "trip_status_history", "", nil, true},
		{"idempotency", "INSERT INTO idempotency_keys (key) VALUES ($1)", insertOperation, "idempotency_keys", "", nil, true},
		{"update", "UPDATE trips SET status = $1", "UPDATE", collection, "", nil, true},
		{"commit", "commit", "COMMIT", "", "", nil, true},
		{"constraint", "INSERT INTO trips (id) VALUES ($1)", insertOperation, collection, "23505", &pgconn.PgError{Code: "23505"}, true},
		{"timeout", selectTrips, selectOperation, collection, "deadline_exceeded", context.DeadlineExceeded, true},
		{"cancel", selectTrips, selectOperation, collection, "canceled", context.Canceled, true},
		{"not found", selectTrips, selectOperation, collection, "", pgx.ErrNoRows, true},
		{"unsampled", selectTrips, selectOperation, collection, "", nil, false},
		{"unknown", "", "UNKNOWN", "", "database_error", errors.New("transport"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			sampler := sdktrace.AlwaysSample()
			if !tc.sampled {
				sampler = sdktrace.NeverSample()
			}
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sampler))
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			oldTP, oldMP := otel.GetTracerProvider(), otel.GetMeterProvider()
			otel.SetTracerProvider(tp)
			otel.SetMeterProvider(mp)
			defer func() {
				otel.SetTracerProvider(oldTP)
				otel.SetMeterProvider(oldMP)
				if err := tp.Shutdown(t.Context()); err != nil {
					t.Error(err)
				}
				if err := mp.Shutdown(t.Context()); err != nil {
					t.Error(err)
				}
			}()
			tracer, err := newQueryTracer()
			if err != nil {
				t.Fatal(err)
			}
			parentCtx, parent := tp.Tracer("test").Start(t.Context(), "HTTP")
			ctx := tracer.TraceQueryStart(parentCtx, nil, pgx.TraceQueryStartData{SQL: tc.sql, Args: []any{"private-value"}})
			tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: tc.err})
			if tc.sampled && !parent.IsRecording() {
				t.Fatal("SQL tracer ended the parent span")
			}
			parent.End()
			spans := exporter.GetSpans()
			if tc.sampled {
				if len(spans) != 2 {
					t.Fatalf("spans = %d, want SQL + HTTP", len(spans))
				}
				query := spans[0]
				if query.Name != tc.operation || query.Parent.SpanID() != parent.SpanContext().SpanID() {
					t.Fatalf("invalid SQL span: %+v", query)
				}
				attrs := attribute.NewSet(query.Attributes...)
				assertQueryAttrs(t, attrs, tc.operation, tc.table, tc.errorType)
				for _, key := range []attribute.Key{"db.query.text", "pgx.query.parameters"} {
					if attrs.HasValue(key) {
						t.Fatalf("sensitive attribute: %s", key)
					}
				}
				if tc.errorType != "" && query.Status.Code != codes.Error {
					t.Fatal("error span not marked")
				}
			} else if len(spans) != 0 {
				t.Fatal("unsampled query produced spans")
			}

			var data metricdata.ResourceMetrics
			if err := reader.Collect(t.Context(), &data); err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, scope := range data.ScopeMetrics {
				for _, instrument := range scope.Metrics {
					if instrument.Name != "db.client.operation.duration" {
						continue
					}
					found++
					histogram := instrument.Data.(metricdata.Histogram[float64])
					if instrument.Unit != "s" || len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Count != 1 {
						t.Fatalf("invalid histogram: %+v", instrument)
					}
					assertQueryAttrs(t, histogram.DataPoints[0].Attributes, tc.operation, tc.table, tc.errorType)
				}
			}
			if found != 1 {
				t.Fatalf("duration instruments = %d, want 1", found)
			}
		})
	}
}

func assertQueryAttrs(t *testing.T, attrs attribute.Set, operation, table, errorType string) {
	t.Helper()
	for key, want := range map[attribute.Key]string{
		"db.operation.name": operation, "db.collection.name": table, "error.type": errorType,
	} {
		got, ok := attrs.Value(key)
		if key == "error.type" && want == "" {
			if ok {
				t.Fatal("error.type present for successful query")
			}
			continue
		}
		if !ok || got.AsString() != want {
			t.Fatalf("%s = %v, want %q", key, got, want)
		}
	}
}
