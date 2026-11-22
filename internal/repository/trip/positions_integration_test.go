//go:build integration

package trip_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/listpositions"
	"github.com/LuCh-Ans/template/internal/usecase/trip/saveposition"
)

func TestSavePosition(t *testing.T) {
	store := newIntegrationStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	trip := integrationTrip()
	if err := store.repo.Create(ctx, trip); err != nil {
		t.Fatal(err)
	}
	latest := trip.StartedAt.Add(time.Minute)
	useCase := saveposition.New(store.tx, store.repo, func() time.Time { return latest })
	position := domain.Position{
		TripID: trip.ID, Coordinates: domain.Point{Latitude: 59.93, Longitude: 30.3},
		RecordedAt: latest,
	}
	for _, at := range []time.Time{latest, latest.Add(-time.Second)} {
		position.RecordedAt = at
		saved, err := useCase.SavePosition(ctx, position)
		if err != nil || saved.ID <= 0 || saved.TripID != trip.ID ||
			saved.Coordinates != position.Coordinates || !saved.RecordedAt.Equal(at) {
			t.Fatalf("SavePosition = %+v, %v", saved, err)
		}
	}

	// This constraint rejects a valid domain point only at the INSERT.
	if _, err := store.pool.Exec(ctx, "ALTER TABLE trip_positions ADD CHECK (latitude <> 0)"); err != nil {
		t.Fatal(err)
	}
	rejected := position
	rejected.RecordedAt = latest.Add(time.Second)
	rejected.Coordinates.Latitude = 0
	var pgErr *pgconn.PgError
	if _, err := useCase.SavePosition(ctx, rejected); !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("expected INSERT check violation, got %v", err)
	}

	got, err := store.repo.GetByID(ctx, trip.ID)
	if err != nil || got.LastPositionAt == nil || !got.LastPositionAt.Equal(latest) {
		t.Fatalf("last position = %v, %v, want %v", got.LastPositionAt, err, latest)
	}

	if _, updated, err := store.repo.FinishActive(ctx, trip.ID, latest); err != nil || !updated {
		t.Fatalf("FinishActive = %v, %v", updated, err)
	}
	if _, err := useCase.SavePosition(ctx, position); !errors.Is(err, domain.ErrTripCompleted) {
		t.Fatalf("completed trip: %v", err)
	}
	position.TripID = uuid.New()
	if _, err := useCase.SavePosition(ctx, position); !errors.Is(err, domain.ErrTripNotFound) {
		t.Fatalf("missing trip: %v", err)
	}
	var count int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM trip_positions").Scan(&count); err != nil || count != 2 {
		t.Fatalf("stored positions = %d, %v, want 2", count, err)
	}
}

func TestListPositions(t *testing.T) {
	store := newIntegrationStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	trip := integrationTrip()
	useCase := listpositions.New(store.repo)
	if _, err := useCase.ListPositions(ctx, trip.ID); !errors.Is(err, domain.ErrTripNotFound) {
		t.Fatalf("missing trip: %v", err)
	}
	if err := store.repo.Create(ctx, trip); err != nil {
		t.Fatal(err)
	}
	if got, err := useCase.ListPositions(ctx, trip.ID); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty route = %+v, %v, want a non-nil empty slice", got, err)
	}

	early := trip.StartedAt.Add(time.Second)
	later := early.Add(time.Second)
	save := saveposition.New(store.tx, store.repo, func() time.Time { return later })
	inserted := make([]domain.Position, 0, 3)
	for _, at := range []time.Time{later, early, early} {
		saved, err := save.SavePosition(ctx, domain.Position{TripID: trip.ID, RecordedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		inserted = append(inserted, saved)
	}
	if _, updated, err := store.repo.FinishActive(ctx, trip.ID, later); err != nil || !updated {
		t.Fatalf("FinishActive = %v, %v", updated, err)
	}

	got, err := useCase.ListPositions(ctx, trip.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Position{inserted[1], inserted[2], inserted[0]}
	if !slices.Equal(got, want) {
		t.Fatalf("completed trip route = %+v, want %+v", got, want)
	}
}
