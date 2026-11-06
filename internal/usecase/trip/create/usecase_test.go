package create_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/create"
)

type transactionContextKey struct{}

func validInput() create.CreateTripInput {
	return create.CreateTripInput{
		UserID: uuid.New(), DriverID: uuid.New(), Price: 1450,
		Start: domain.Point{Latitude: 59.9, Longitude: 30.3},
		End:   domain.Point{Latitude: 60, Longitude: 31},
	}
}

func TestCreateTrip(t *testing.T) {
	failure := errors.New("database failure")
	tests := []struct {
		name       string
		insertErr  error
		historyErr error
		txErr      error
		wantErr    error
	}{
		{name: "success"},
		{name: "driver busy", insertErr: domain.ErrDriverBusy, wantErr: domain.ErrDriverBusy},
		{name: "insert failure", insertErr: failure, wantErr: failure},
		{name: "history failure", historyErr: failure, wantErr: failure},
		{name: "transaction failure", txErr: failure, wantErr: failure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := NewMocktripRepository(ctrl)
			tx := NewMocktxManager(ctrl)
			ctx := t.Context()
			txCtx := context.WithValue(ctx, transactionContextKey{}, "transaction")
			input := validInput()
			now := time.Date(2026, 11, 6, 12, 0, 0, 987654321, time.FixedZone("local", 3*3600))
			var saved domain.Trip
			if tt.txErr != nil {
				tx.EXPECT().Do(ctx, gomock.Any()).Return(tt.txErr)
			} else {
				tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
					return fn(txCtx)
				})
				insert := repo.EXPECT().Create(txCtx, gomock.Any()).DoAndReturn(func(_ context.Context, trip domain.Trip) error {
					saved = trip
					want := domain.Trip{
						ID: trip.ID, UserID: input.UserID, DriverID: input.DriverID, Start: input.Start,
						End: input.End, Price: input.Price, Status: domain.StatusActive,
						StartedAt: now.UTC().Truncate(time.Microsecond),
					}
					if trip.ID == uuid.Nil || !reflect.DeepEqual(trip, want) {
						t.Fatalf("saved trip = %#v, want %#v with nonzero ID", trip, want)
					}
					return tt.insertErr
				})
				if tt.insertErr == nil {
					repo.EXPECT().AddStatusChange(txCtx, gomock.Any(), nil, domain.StatusActive, "trip created").
						DoAndReturn(func(_ context.Context, id uuid.UUID, _ *domain.TripStatus, _ domain.TripStatus, _ string) error {
							if id != saved.ID {
								t.Fatalf("history trip ID = %s, want %s", id, saved.ID)
							}
							return tt.historyErr
						}).After(insert)
				}
			}
			got, created, err := create.New(tx, repo, time.Hour, func() time.Time { return now }).CreateTrip(ctx, input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if created || !reflect.DeepEqual(got, domain.Trip{}) {
					t.Fatalf("failed create returned %#v, created=%v", got, created)
				}
				return
			}
			if !created || !reflect.DeepEqual(got, saved) {
				t.Fatalf("result = %#v, created=%v, want saved trip", got, created)
			}
		})
	}
}

func TestCreateTripValidationBeforeTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocktripRepository(ctrl)
	tx := NewMocktxManager(ctrl)
	input := validInput()
	input.Price = -1
	got, created, err := create.New(tx, repo, time.Hour, time.Now).CreateTrip(t.Context(), input)
	var validationErr *domain.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "price" {
		t.Fatalf("error = %v, want price validation error", err)
	}
	if created || !reflect.DeepEqual(got, domain.Trip{}) {
		t.Fatalf("invalid input returned %#v, created=%v", got, created)
	}
	// No mock expectations: accessing the transaction or repository fails the test.
}

func TestCreateTripIdempotencyReplay(t *testing.T) {
	failure := errors.New("idempotency storage failed")
	tests := []struct {
		name     string
		claimErr error
		keyErr   error
		tripErr  error
		conflict bool
		wantErr  error
	}{
		{name: "same payload returns existing trip"},
		{name: "different payload", conflict: true, wantErr: domain.ErrIdempotencyConflict},
		{name: "claim failure", claimErr: failure, wantErr: failure},
		{name: "key read failure", keyErr: failure, wantErr: failure},
		{name: "trip read failure", tripErr: failure, wantErr: failure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := NewMocktripRepository(ctrl)
			tx := NewMocktxManager(ctrl)
			ctx := t.Context()
			txCtx := context.WithValue(ctx, transactionContextKey{}, "transaction")
			key := uuid.New()
			input := validInput()
			input.IdempotencyKey = &key
			existing := domain.Trip{ID: uuid.New(), DriverID: input.DriverID, Status: domain.StatusActive}
			var hash string
			tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
				return fn(txCtx)
			})
			claim := repo.EXPECT().ClaimIdempotencyKey(txCtx, key, gomock.Any(), gomock.Any(), time.Hour).
				DoAndReturn(func(_ context.Context, _ uuid.UUID, requestHash string, id uuid.UUID, _ time.Duration) (bool, error) {
					if len(requestHash) != 64 || id == uuid.Nil {
						t.Fatalf("invalid claim hash or trip ID: %q, %s", requestHash, id)
					}
					hash = requestHash
					return false, tt.claimErr
				})
			if tt.claimErr == nil {
				read := repo.EXPECT().GetIdempotencyKey(txCtx, key).
					DoAndReturn(func(context.Context, uuid.UUID) (string, uuid.UUID, error) {
						storedHash := hash
						if tt.conflict {
							storedHash = "different-payload"
						}
						return storedHash, existing.ID, tt.keyErr
					}).After(claim)
				if tt.keyErr == nil && !tt.conflict {
					repo.EXPECT().GetByID(txCtx, existing.ID).Return(existing, tt.tripErr).After(read)
				}
			}
			got, created, err := create.New(tx, repo, time.Hour, time.Now).CreateTrip(ctx, input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			want := existing
			if tt.wantErr != nil {
				want = domain.Trip{}
			}
			if created || !reflect.DeepEqual(got, want) {
				t.Fatalf("result = %#v, created=%v, want %#v and false", got, created, want)
			}
		})
	}
}

func TestCreateTripClaimsKeyBeforeInsert(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMocktripRepository(ctrl)
	tx := NewMocktxManager(ctrl)
	ctx := t.Context()
	input := validInput()
	key := uuid.New()
	input.IdempotencyKey = &key
	var claimedID uuid.UUID
	var saved domain.Trip

	tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
		return fn(ctx)
	})
	claim := repo.EXPECT().ClaimIdempotencyKey(ctx, key, gomock.Any(), gomock.Any(), time.Hour).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, _ string, id uuid.UUID, _ time.Duration) (bool, error) {
			claimedID = id
			return true, nil
		})
	insert := repo.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, trip domain.Trip) error {
		saved = trip
		if trip.ID != claimedID || claimedID == uuid.Nil {
			t.Fatalf("trip ID %s does not match claimed ID %s", trip.ID, claimedID)
		}
		return nil
	}).After(claim)
	repo.EXPECT().AddStatusChange(ctx, gomock.Any(), nil, domain.StatusActive, "trip created").
		DoAndReturn(func(_ context.Context, id uuid.UUID, _ *domain.TripStatus, _ domain.TripStatus, _ string) error {
			if id != claimedID {
				t.Fatalf("history ID = %s, want %s", id, claimedID)
			}
			return nil
		}).After(insert)
	got, created, err := create.New(tx, repo, time.Hour, time.Now).CreateTrip(ctx, input)
	if err != nil || !created || !reflect.DeepEqual(got, saved) {
		t.Fatalf("create = %#v, %v, %v; want %#v, true, nil", got, created, err, saved)
	}
}

func idempotencyHash(t *testing.T, input create.CreateTripInput) string {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMocktripRepository(ctrl)
	tx := NewMocktxManager(ctrl)
	ctx := t.Context()
	key := uuid.New()
	input.IdempotencyKey = &key
	var hash string
	existing := domain.Trip{ID: uuid.New()}
	tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
		return fn(ctx)
	})
	repo.EXPECT().ClaimIdempotencyKey(ctx, key, gomock.Any(), gomock.Any(), time.Hour).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, requestHash string, _ uuid.UUID, _ time.Duration) (bool, error) {
			hash = requestHash
			return false, nil
		})
	repo.EXPECT().GetIdempotencyKey(ctx, key).DoAndReturn(func(context.Context, uuid.UUID) (string, uuid.UUID, error) {
		return hash, existing.ID, nil
	})
	repo.EXPECT().GetByID(ctx, existing.ID).Return(existing, nil)
	got, created, err := create.New(tx, repo, time.Hour, time.Now).CreateTrip(ctx, input)
	if err != nil || created || got.ID != existing.ID || len(hash) != 64 {
		t.Fatalf("replay = %#v, %v, %v, hash=%q", got, created, err, hash)
	}
	return hash
}

func TestIdempotencyHashUsesRequestFields(t *testing.T) {
	base := validInput()
	baselineHash := idempotencyHash(t, base)
	tests := []struct {
		name   string
		change func(*create.CreateTripInput)
	}{
		{name: "same body with a different key"},
		{name: "user", change: func(in *create.CreateTripInput) { in.UserID = uuid.New() }},
		{name: "driver", change: func(in *create.CreateTripInput) { in.DriverID = uuid.New() }},
		{name: "start latitude", change: func(in *create.CreateTripInput) { in.Start.Latitude++ }},
		{name: "start longitude", change: func(in *create.CreateTripInput) { in.Start.Longitude++ }},
		{name: "end latitude", change: func(in *create.CreateTripInput) { in.End.Latitude++ }},
		{name: "end longitude", change: func(in *create.CreateTripInput) { in.End.Longitude++ }},
		{name: "price", change: func(in *create.CreateTripInput) { in.Price++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			if tt.change != nil {
				tt.change(&input)
			}
			hash := idempotencyHash(t, input)
			if tt.change == nil && hash != baselineHash {
				t.Error("same body produced a different hash")
			}
			if tt.change != nil && hash == baselineHash {
				t.Error("changed request field did not change the hash")
			}
		})
	}
}
