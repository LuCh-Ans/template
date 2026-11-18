package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

type queryContextKey struct{}

type queryObservation struct {
	started    time.Time
	operation  string
	collection string
}

type queryTracer struct {
	base     *otelpgx.Tracer
	duration metric.Float64Histogram
}

var _ pgx.QueryTracer = (*queryTracer)(nil)

func newQueryTracer() (*queryTracer, error) {
	duration, err := otel.Meter("trip-service/postgres").
		Float64Histogram(
			"db.client.operation.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Duration of PostgreSQL operations"),
			metric.WithExplicitBucketBoundaries(
				0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10,
			),
		)
	if err != nil {
		return nil, fmt.Errorf("create database duration metric: %w", err)
	}

	base := otelpgx.NewTracer(
		// Метрику с нужными метками записываем самостоятельно
		otelpgx.WithMeterProvider(noop.NewMeterProvider()),
		otelpgx.WithDisableSQLStatementInAttributes(),
		otelpgx.WithDisableConnectionDetailsInAttributes(),
		otelpgx.WithDisableQuerySpanNamePrefix(),
		otelpgx.WithTrimSQLInSpanName(),
		otelpgx.WithSpanNameFunc(queryOperation),
	)

	return &queryTracer{
		base:     base,
		duration: duration,
	}, nil
}

func (t *queryTracer) TraceQueryStart(
	ctx context.Context,
	conn *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	observation := queryObservation{
		started:    time.Now(),
		operation:  queryOperation(data.SQL),
		collection: queryCollection(data.SQL),
	}

	ctx = t.base.TraceQueryStart(ctx, conn, data)

	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("db.operation.name", observation.operation),
		attribute.String("db.collection.name", observation.collection),
	)

	return context.WithValue(ctx, queryContextKey{}, observation)
}

func (t *queryTracer) TraceQueryEnd(
	ctx context.Context,
	conn *pgx.Conn,
	data pgx.TraceQueryEndData,
) {
	observation, ok := ctx.Value(queryContextKey{}).(queryObservation)
	if ok {
		attrs := []attribute.KeyValue{
			attribute.String("db.operation.name", observation.operation),
			attribute.String("db.collection.name", observation.collection),
		}

		if errorType := queryErrorType(data.Err); errorType != "" {
			errorAttr := attribute.String("error.type", errorType)
			attrs = append(attrs, errorAttr)

			span := trace.SpanFromContext(ctx)
			span.SetAttributes(errorAttr)
			span.SetStatus(codes.Error, errorType)
		}

		t.duration.Record(
			ctx,
			time.Since(observation.started).Seconds(),
			metric.WithAttributes(attrs...),
		)
	}

	t.base.TraceQueryEnd(ctx, conn, data)
}

func queryOperation(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "UNKNOWN"
	}

	switch operation := strings.ToUpper(fields[0]); operation {
	case "SELECT", "INSERT", "UPDATE", "DELETE",
		"BEGIN", "COMMIT", "ROLLBACK":
		return operation
	default:
		return "UNKNOWN"
	}
}

// Распознаём только формы запросов и таблицы нашего сервиса
// Это не универсальный SQL-парсер
func queryCollection(sql string) string {
	fields := strings.Fields(sql)

	for i := 0; i+1 < len(fields); i++ {
		switch strings.ToUpper(fields[i]) {
		case "FROM", "INTO", "UPDATE":
			table := strings.ToLower(strings.Trim(fields[i+1], "\"(),"))
			switch table {
			case "trips", "trip_status_history", "idempotency_keys":
				return table
			}
		}
	}

	// У BEGIN, COMMIT и ROLLBACK нет таблицы
	return ""
}

func queryErrorType(err error) string {
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}

	return "database_error"
}
