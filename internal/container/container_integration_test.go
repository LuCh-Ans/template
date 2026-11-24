//go:build integration

package container

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/goleak"

	"github.com/LuCh-Ans/template/internal/config"
	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/infra/postgres/transaction"
	triprepo "github.com/LuCh-Ans/template/internal/repository/trip"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func isolatedDatabase(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		t.Fatal("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_positions_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+quoted); err != nil {
		t.Fatal(err)
	}
	files, err := fs.ReadDir(os.DirFS("../../migrations"), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		content, err := os.ReadFile("../../migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		up, _, found := strings.Cut(string(content), "-- +goose Down")
		if !found {
			t.Fatalf("migration %s has no Down section", file.Name())
		}
		if _, err := admin.Exec(ctx, up); err != nil {
			t.Fatal(err)
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func TestContainerPositionsPipelineAndShutdown(t *testing.T) {
	database := isolatedDatabase(t)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)
	received := make(chan map[string]any, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releasePush := func() { releaseOnce.Do(func() { close(release) }) }
	var accepted atomic.Bool
	pushServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		select {
		case received <- payload:
		case <-r.Context().Done():
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		accepted.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(pushServer.Close)
	cfg := config.Config{
		HTTP:            config.HTTPConfig{Addr: "127.0.0.1:0", ReadTimeout: time.Second, ReadHeaderTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second},
		DB:              config.DBConfig{URL: database, MaxConns: 4, ConnectTimeout: time.Second, QueryTimeout: time.Second, MaxConnLifetime: time.Hour},
		Telemetry:       config.TelemetryConfig{ServiceName: "trip-service", ServiceVersion: "test", Endpoint: collector.URL, Protocol: "http/protobuf", ExportTimeout: time.Second, BatchTimeout: time.Second},
		Positions:       config.PositionsConfig{Workers: 1, QueueSize: 4, CheckInterval: 10 * time.Millisecond, StaleAfter: time.Hour},
		Push:            config.PushConfig{HTTPURL: pushServer.URL, RequestTimeout: 5 * time.Second},
		ShutdownTimeout: 5 * time.Second, IdempotencyTTL: time.Hour,
	}
	app, err := New(t.Context(), cfg)
	if err != nil {
		releasePush()
		t.Fatal(err)
	}
	var closeOnce sync.Once
	var closeErr error
	closeApp := func() {
		closeOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
			defer cancel()
			closeErr = app.Close(ctx)
		})
	}
	t.Cleanup(func() {
		releasePush()
		closeApp()
		if closeErr != nil {
			t.Error(closeErr)
		}
	})
	repo := triprepo.NewTripRepository(transaction.NewTxManager(app.pool, time.Second), time.Second)
	trip := domain.Trip{
		ID: uuid.New(), UserID: uuid.New(), DriverID: uuid.New(),
		Start: domain.Point{Latitude: 59.9, Longitude: 30.3}, End: domain.Point{Latitude: 60, Longitude: 31},
		Price: 1450, Status: domain.StatusActive, StartedAt: time.Now().UTC().Add(-2 * time.Hour),
	}
	if err := repo.Create(t.Context(), trip); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.WithoutCancel(t.Context())); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Server.Handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), cfg.ShutdownTimeout)
	defer cancel()
	var payload map[string]any
	select {
	case payload = <-received:
	case <-ctx.Done():
		t.Fatal("scanner did not reach Push Service")
	}
	data, ok := payload["data"].(map[string]any)
	if !ok || data["trip_id"] != trip.ID.String() || payload["recipient_id"] != trip.DriverID.String() || payload["request_id"] == "" {
		t.Fatalf("invalid push payload: %+v", payload)
	}
	position := `{"coordinates":{"latitude":59.93,"longitude":30.3},"recorded_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/trips/"+trip.ID.String()+"/positions", strings.NewReader(position))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("save position status=%d", response.StatusCode)
	}
	stored, err := repo.GetByID(ctx, trip.ID)
	if err != nil || stored.LastPositionAt == nil {
		t.Fatalf("position not saved: %v", err)
	}
	flushTelemetry := app.shutdownTelemetry
	app.shutdownTelemetry = func(ctx context.Context) error {
		if !accepted.Load() {
			t.Error("resources closed before active Push completed")
		}
		return flushTelemetry(ctx)
	}
	if err := server.Config.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); closeApp() }()
	releasePush()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("service did not stop within shutdown budget")
	}
	if closeErr != nil || app.pool.Stat().TotalConns() != 0 {
		t.Fatalf("close error=%v, connections=%d", closeErr, app.pool.Stat().TotalConns())
	}
}
