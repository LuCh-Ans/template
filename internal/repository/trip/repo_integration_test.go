//go:build integration

package trip_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/infra/postgres/transaction"
	triprepo "github.com/LuCh-Ans/template/internal/repository/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/create"
	"github.com/LuCh-Ans/template/internal/usecase/trip/finish"
)

type noopTripMetrics struct{}

func (noopTripMetrics) TripCreated(context.Context) {}

func (noopTripMetrics) TripCompleted(context.Context) {}

type integrationStore struct {
	repo *triprepo.TripRepository
	tx   *transaction.TxManager
	pool *pgxpool.Pool
}

func newIntegrationStore(t *testing.T) integrationStore {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is required; start tripgoctl and run make test-integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "test_trip_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("cleanup schema: %v", err)
		}
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	migrations := os.DirFS("../../../migrations")
	files, err := fs.ReadDir(migrations, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		content, err := fs.ReadFile(migrations, file.Name())
		if err != nil {
			t.Fatal(err)
		}
		up, _, found := strings.Cut(string(content), "-- +goose Down")
		if !found {
			t.Fatalf("migration %s has no Down section", file.Name())
		}
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("migration %s: %v", file.Name(), err)
		}
	}
	tx := transaction.NewTxManager(pool, 5*time.Second)
	return integrationStore{repo: triprepo.NewTripRepository(tx, 5*time.Second), tx: tx, pool: pool}
}

func integrationTrip() domain.Trip {
	return domain.Trip{
		ID: uuid.New(), UserID: uuid.New(), DriverID: uuid.New(),
		Start: domain.Point{Latitude: 59.9, Longitude: 30.3},
		End:   domain.Point{Latitude: 60, Longitude: 31},
		Price: 1450, Status: domain.StatusActive,
		StartedAt: time.Date(2026, 11, 6, 12, 0, 0, 123456000, time.UTC),
	}
}

func assertRowCounts(t *testing.T, pool *pgxpool.Pool, trips, history, keys int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var gotTrips, gotHistory, gotKeys int
	err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM trips), (SELECT count(*) FROM trip_status_history), (SELECT count(*) FROM idempotency_keys)").
		Scan(&gotTrips, &gotHistory, &gotKeys)
	if err != nil {
		t.Fatal(err)
	}
	if gotTrips != trips || gotHistory != history || gotKeys != keys {
		t.Fatalf("counts = (%d,%d,%d), want (%d,%d,%d)", gotTrips, gotHistory, gotKeys, trips, history, keys)
	}
}

func TestRepositoryLifecycle(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := t.Context()
	trip := integrationTrip()
	err := store.tx.Do(ctx, func(ctx context.Context) error {
		if err := store.repo.Create(ctx, trip); err != nil {
			return err
		}
		return store.repo.AddStatusChange(ctx, trip.ID, nil, domain.StatusActive, "created")
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.repo.GetByID(ctx, trip.ID)
	if err != nil || got != trip {
		t.Fatalf("GetByID = %#v, %v; want %#v", got, err, trip)
	}
	busy := trip
	busy.ID = uuid.New()
	if err := store.repo.Create(ctx, busy); !errors.Is(err, domain.ErrDriverBusy) {
		t.Fatalf("duplicate driver error = %v", err)
	}
	if _, err := store.repo.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrTripNotFound) {
		t.Fatalf("missing error = %v", err)
	}
	if count, err := store.repo.CountActive(ctx); err != nil || count != 1 {
		t.Fatalf("active before finish = %d, %v", count, err)
	}
	at := trip.StartedAt.Add(time.Minute)
	completed, err := finish.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), noopTripMetrics{}, store.tx, store.repo, func() time.Time { return at }).FinishTrip(ctx, trip.ID)
	if err != nil || completed.Status != domain.StatusCompleted || completed.FinishedAt == nil || !completed.FinishedAt.Equal(at) {
		t.Fatalf("finish = %#v, %v", completed, err)
	}
	if _, err := finish.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), noopTripMetrics{}, store.tx, store.repo, func() time.Time { return at.Add(time.Hour) }).FinishTrip(ctx, trip.ID); !errors.Is(err, domain.ErrTripCompleted) {
		t.Fatalf("second finish error = %v", err)
	}
	if count, err := store.repo.CountActive(ctx); err != nil || count != 0 {
		t.Fatalf("active after finish = %d, %v", count, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.repo.CountActive(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("count did not honor cancellation: %v", err)
	}
	got, err = store.repo.GetByID(ctx, trip.ID)
	if err != nil || got.FinishedAt == nil || !got.FinishedAt.Equal(at) {
		t.Fatalf("stored finish time changed: %#v, %v", got, err)
	}
	assertRowCounts(t, store.pool, 1, 2, 0)
}

func TestRepositoryRollbackBothTables(t *testing.T) {
	store := newIntegrationStore(t)
	trip := integrationTrip()
	ctx := t.Context()
	err := store.tx.Do(ctx, func(ctx context.Context) error {
		if err := store.repo.Create(ctx, trip); err != nil {
			return err
		}
		// The second INSERT violates the real history table's status constraint
		return store.repo.AddStatusChange(ctx, trip.ID, nil, domain.TripStatus("invalid"), "forced failure")
	})
	if err == nil {
		t.Fatal("expected history INSERT to fail")
	}
	if _, err := store.repo.GetByID(ctx, trip.ID); !errors.Is(err, domain.ErrTripNotFound) {
		t.Fatalf("trip survived rollback: %v", err)
	}
	assertRowCounts(t, store.pool, 0, 0, 0)
}

func TestRepositoryIdempotency(t *testing.T) {
	store := newIntegrationStore(t)
	trip := integrationTrip()
	ctx := t.Context()
	key := uuid.New()
	input := create.CreateTripInput{
		UserID: trip.UserID, DriverID: trip.DriverID, Start: trip.Start, End: trip.End,
		Price: trip.Price, IdempotencyKey: &key,
	}
	usecase := create.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), noopTripMetrics{}, store.tx, store.repo, time.Hour, func() time.Time { return trip.StartedAt })
	first, created, err := usecase.CreateTrip(ctx, input)
	if err != nil || !created {
		t.Fatalf("first create: created=%v, error=%v", created, err)
	}
	replay, created, err := usecase.CreateTrip(ctx, input)
	if err != nil || created || replay != first {
		t.Fatalf("replay = %#v, created=%v, error=%v", replay, created, err)
	}
	changed := input
	changed.Price++
	if _, _, err := usecase.CreateTrip(ctx, changed); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("different payload error = %v", err)
	}
	secondKey := uuid.New()
	changed.IdempotencyKey = &secondKey
	if _, _, err := usecase.CreateTrip(ctx, changed); !errors.Is(err, domain.ErrDriverBusy) {
		t.Fatalf("busy driver error = %v", err)
	}
	assertRowCounts(t, store.pool, 1, 1, 1)

	at := trip.StartedAt.Add(time.Minute)
	if _, err := finish.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), noopTripMetrics{}, store.tx, store.repo, func() time.Time { return at }).FinishTrip(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "UPDATE idempotency_keys SET created_at = now() - interval '2 hours' WHERE key = $1", key); err != nil {
		t.Fatal(err)
	}
	next, created, err := usecase.CreateTrip(ctx, input)
	if err != nil || !created || next.ID == first.ID {
		t.Fatalf("expired key: %#v, created=%v, error=%v", next, created, err)
	}
	assertRowCounts(t, store.pool, 2, 3, 1)
}

func TestRepositoryConcurrentFinish(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := t.Context()
	trip := integrationTrip()
	if err := store.repo.Create(ctx, trip); err != nil {
		t.Fatal(err)
	}
	at := trip.StartedAt.Add(time.Minute)
	usecase := finish.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), noopTripMetrics{}, store.tx, store.repo, func() time.Time { return at })
	const workers = 8
	start := make(chan struct{})
	results := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			completed, err := usecase.FinishTrip(ctx, trip.ID)
			if err == nil && (completed.FinishedAt == nil || !completed.FinishedAt.Equal(at)) {
				err = fmt.Errorf("unexpected completed trip: %#v", completed)
			}
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrTripCompleted):
			conflicts++
		default:
			t.Fatalf("unexpected finish error: %v", err)
		}
	}
	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("successes=%d, conflicts=%d", successes, conflicts)
	}
	assertRowCounts(t, store.pool, 1, 1, 0)
}

func TestTransactionRollbackAfterPanic(t *testing.T) {
	store := newIntegrationStore(t)
	trip := integrationTrip()
	const panicMessage = "test panic"
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		err := store.tx.Do(t.Context(), func(ctx context.Context) error {
			if err := store.repo.Create(ctx, trip); err != nil {
				return err
			}
			panic(panicMessage)
		})
		if err != nil {
			t.Fatalf("transaction failed before panic: %v", err)
		}
	}()
	if recovered != panicMessage {
		t.Fatalf("panic = %v, want %q", recovered, panicMessage)
	}
	assertRowCounts(t, store.pool, 0, 0, 0)
}

func TestTransactionRollbackAfterCancellation(t *testing.T) {
	store := newIntegrationStore(t)
	trip := integrationTrip()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := store.tx.Do(ctx, func(ctx context.Context) error {
		if err := store.repo.Create(ctx, trip); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	assertRowCounts(t, store.pool, 0, 0, 0)
}
