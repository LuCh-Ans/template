package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

//go:generate go tool mockgen -destination=mocks_test.go -package=push net/http RoundTripper
//go:generate go tool mockgen -destination=body_mocks_test.go -package=push io ReadCloser

const maxResponseDrain = 64 << 10

type Gateway struct {
	endpoint string
	client   *http.Client
	tracer   trace.Tracer
}

func New(baseURL string, timeout time.Duration) (*Gateway, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse push URL: %w", err)
	}
	if endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("push URL must be an absolute HTTP or HTTPS URL")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("push timeout must be positive, got %s", timeout)
	}
	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: timeout, KeepAlive: timeout}).DialContext,
		ForceAttemptHTTP2: true, TLSHandshakeTimeout: timeout,
		ResponseHeaderTimeout: timeout, IdleConnTimeout: timeout,
	}
	return &Gateway{
		endpoint: endpoint.JoinPath("api/v1/push").String(),
		client: &http.Client{
			Transport: transport, Timeout: timeout,
			// Редирект — тоже ответ сервера; повторный запрос здесь не отправляем.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		tracer: otel.Tracer("trip-service/push"),
	}, nil
}

func (g *Gateway) RequestPosition(ctx context.Context, tripID, driverID uuid.UUID, requestID string) (err error) {
	ctx, span := g.tracer.Start(ctx, "push.request_position", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("http.request.method", http.MethodPost), attribute.String("request_id", requestID)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "push request failed")
			span.SetAttributes(attribute.String("error.type", fmt.Sprintf("%T", err)))
		}
		span.End()
	}()
	payload := struct {
		RequestID   string            `json:"request_id"`
		RecipientID uuid.UUID         `json:"recipient_id"`
		Kind        string            `json:"kind"`
		Data        map[string]string `json:"data"`
	}{RequestID: requestID, RecipientID: driverID, Kind: "REQUEST_POSITION", Data: map[string]string{"trip_id": tripID.String()}}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode position request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create position request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	resp, err := g.client.Do(req)
	if err != nil {
		return &TransportError{Err: err}
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	defer func() {
		// Ограниченно дочитываем ответ для переиспользования соединения.
		// При ошибке HTTP сохраняем её код, независимо от ошибок чтения тела.
		_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseDrain))
		closeErr := resp.Body.Close()
		if err == nil && readErr != nil {
			err = &TransportError{Err: fmt.Errorf("read push response: %w", readErr)}
		}
		if err == nil && closeErr != nil {
			err = &TransportError{Err: fmt.Errorf("close push response: %w", closeErr)}
		}
	}()
	if resp.StatusCode != http.StatusAccepted {
		return &StatusError{StatusCode: resp.StatusCode}
	}
	return nil
}

// Close вызывается после завершения воркеров.
func (g *Gateway) Close() { g.client.CloseIdleConnections() }
