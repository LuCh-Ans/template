package telemetry

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// NewLogger создаёт JSON-логгер с корреляцией по контексту текущего спана.
func NewLogger(out io.Writer, level slog.Level, service string) *slog.Logger {
	handler := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})
	return slog.New(&traceHandler{Handler: handler}).With("service", service)
}

type traceHandler struct {
	slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		record = record.Clone()
		record.AddAttrs(slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
	}
	return h.Handler.Handle(ctx, record)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}
