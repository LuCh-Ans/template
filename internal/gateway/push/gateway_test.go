package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func newTestGateway(t *testing.T, endpoint string) *Gateway {
	t.Helper()
	gateway, err := New(endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	return gateway
}

func TestHTTPContractAndTracePropagation(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider, previousPropagation := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagation)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	tripID, driverID, requestID := uuid.New(), uuid.New(), uuid.NewString()
	received := make(chan trace.SpanContext, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/prefix/api/v1/push" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("invalid push request: %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		data, ok := got["data"].(map[string]any)
		if !ok || len(got) != 4 || len(data) != 1 || data["trip_id"] != tripID.String() ||
			got["request_id"] != requestID || got["recipient_id"] != driverID.String() || got["kind"] != "REQUEST_POSITION" {
			t.Errorf("invalid payload: %+v", got)
		}
		received <- trace.SpanContextFromContext(propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header)))
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	gateway := newTestGateway(t, server.URL+"/prefix/")
	ctx, worker := provider.Tracer("test").Start(t.Context(), "positions.request")
	if err := gateway.RequestPosition(ctx, tripID, driverID, requestID); err != nil {
		t.Fatal(err)
	}
	worker.End()
	incoming := <-received
	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans=%+v", spans)
	}
	push := spans[0]
	if push.Name() != "push.request_position" || push.SpanKind() != trace.SpanKindClient ||
		!push.Parent().Equal(worker.SpanContext()) || (!incoming.IsRemote() || !incoming.WithRemote(false).Equal(push.SpanContext())) {
		t.Fatalf("invalid push span or propagation: %+v, incoming=%v", push, incoming)
	}
	if push.Status().Code == codes.Error {
		t.Fatal("accepted push marked as error")
	}
}

func TestHTTPStatusesCloseBodyAndDoNotRetry(t *testing.T) {
	for _, status := range []int{202, 200, 204, 302, 400, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			transport, body := NewMockRoundTripper(ctrl), NewMockReadCloser(ctrl)
			body.EXPECT().Read(gomock.Any()).Return(0, io.EOF)
			body.EXPECT().Close().Return(nil)
			transport.EXPECT().RoundTrip(gomock.Any()).Return(&http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil)
			gateway := newTestGateway(t, "http://push.test")
			gateway.client.Transport = transport
			err := gateway.RequestPosition(t.Context(), uuid.New(), uuid.New(), "request-id")
			if status == http.StatusAccepted {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var statusErr *StatusError
			var transportErr *TransportError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != status || errors.As(err, &transportErr) || err.Error() == "" {
				t.Fatalf("error=%v, want HTTP %d", err, status)
			}
		})
	}
}

func TestTransportErrorPreservesCause(t *testing.T) {
	failure := errors.New("network unavailable")
	transport := NewMockRoundTripper(gomock.NewController(t))
	transport.EXPECT().RoundTrip(gomock.Any()).Return(nil, failure)
	gateway := newTestGateway(t, "http://push.test")
	gateway.client.Transport = transport
	err := gateway.RequestPosition(t.Context(), uuid.New(), uuid.New(), "request-id")
	var transportErr *TransportError
	var statusErr *StatusError
	if !errors.Is(err, failure) || !errors.As(err, &transportErr) || errors.As(err, &statusErr) || err.Error() == "" {
		t.Fatalf("transport error=%v", err)
	}
}

func TestBodyErrorsStillCloseResponse(t *testing.T) {
	failure := errors.New("body failure")
	for _, tt := range []struct {
		name              string
		status            int
		readErr, closeErr error
	}{
		{name: "read failure", status: 202, readErr: failure},
		{name: "close failure", status: 202, readErr: io.EOF, closeErr: failure},
		{name: "HTTP status takes precedence", status: 503, readErr: failure, closeErr: failure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			transport, body := NewMockRoundTripper(ctrl), NewMockReadCloser(ctrl)
			body.EXPECT().Read(gomock.Any()).Return(0, tt.readErr)
			body.EXPECT().Close().Return(tt.closeErr)
			transport.EXPECT().RoundTrip(gomock.Any()).Return(&http.Response{StatusCode: tt.status, Body: body}, nil)
			gateway := newTestGateway(t, "http://push.test")
			gateway.client.Transport = transport
			err := gateway.RequestPosition(t.Context(), uuid.New(), uuid.New(), "request-id")
			if tt.status == 202 {
				var transportErr *TransportError
				if !errors.As(err, &transportErr) || !errors.Is(err, failure) {
					t.Fatalf("body error=%v", err)
				}
			} else {
				var statusErr *StatusError
				if !errors.As(err, &statusErr) || statusErr.StatusCode != tt.status {
					t.Fatalf("status error=%v", err)
				}
			}
		})
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller cancellation", true: "client timeout"}[timeout], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				transport := NewMockRoundTripper(gomock.NewController(t))
				entered := make(chan struct{})
				transport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
					close(entered)
					<-req.Context().Done()
					return nil, req.Context().Err()
				})
				gateway := newTestGateway(t, "http://push.test")
				gateway.client.Transport = transport
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				result := make(chan error, 1)
				start := time.Now()
				go func() { result <- gateway.RequestPosition(ctx, uuid.New(), uuid.New(), "request-id") }()
				<-entered
				want := context.DeadlineExceeded
				if !timeout {
					cancel()
					want = context.Canceled
				}
				err := <-result
				var transportErr *TransportError
				if !errors.Is(err, want) || !errors.As(err, &transportErr) {
					t.Fatalf("cancellation error=%v", err)
				}
				if timeout && time.Since(start) != time.Second {
					t.Fatalf("elapsed=%s", time.Since(start))
				}
			})
		})
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	gateway := newTestGateway(t, server.URL)
	err := gateway.RequestPosition(t.Context(), uuid.New(), uuid.New(), "request-id")
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != 307 || calls.Load() != 1 {
		t.Fatalf("redirect error=%v, calls=%d", err, calls.Load())
	}
}

func TestGatewayConfig(t *testing.T) {
	for _, endpoint := range []string{":bad", "/relative", "ftp://push.test"} {
		if _, err := New(endpoint, time.Second); err == nil {
			t.Fatalf("invalid URL %q accepted", endpoint)
		}
	}
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, err := New("http://push.test", timeout); err == nil {
			t.Fatalf("invalid timeout %s accepted", timeout)
		}
	}
	gateway := newTestGateway(t, "http://push.test")
	if gateway.client == http.DefaultClient || gateway.client.Transport == http.DefaultTransport || gateway.client.Timeout != time.Second {
		t.Fatal("gateway does not own its configured HTTP client")
	}
}
