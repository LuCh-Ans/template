package finish_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/finish"
)

type transactionContextKey struct{}

func TestFinishTrip(t *testing.T) {
	failure := errors.New("database failure")
	tests := []struct {
		name       string
		status     domain.TripStatus
		readErr    error
		updateErr  error
		historyErr error
		txErr      error
		updated    bool
		wantErr    error
	}{
		{name: "success", status: domain.StatusActive, updated: true},
		{name: "already completed", status: domain.StatusCompleted, wantErr: domain.ErrTripCompleted},
		{name: "missing", readErr: domain.ErrTripNotFound, wantErr: domain.ErrTripNotFound},
		{name: "read failure", readErr: failure, wantErr: failure},
		{name: "update failure", status: domain.StatusActive, updateErr: failure, wantErr: failure},
		{name: "concurrent finish", status: domain.StatusActive, wantErr: domain.ErrTripCompleted},
		{name: "history failure", status: domain.StatusActive, updated: true, historyErr: failure, wantErr: failure},
		{name: "transaction failure", txErr: failure, wantErr: failure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := NewMocktripRepository(ctrl)
			tx := NewMocktxManager(ctrl)
			ctx := t.Context()
			txCtx := context.WithValue(ctx, transactionContextKey{}, "transaction")
			now := time.Date(2026, 11, 6, 12, 0, 0, 123456789, time.FixedZone("local", 3*3600))
			at := now.UTC().Truncate(time.Microsecond)
			id := uuid.New()
			current := domain.Trip{ID: id, Status: tt.status}
			completed := domain.Trip{ID: id, Status: domain.StatusCompleted, FinishedAt: &at}
			if tt.txErr != nil {
				tx.EXPECT().Do(ctx, gomock.Any()).Return(tt.txErr)
			} else {
				tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
					return fn(txCtx)
				})
				read := repo.EXPECT().GetByID(txCtx, id).Return(current, tt.readErr)
				if tt.readErr == nil && tt.status == domain.StatusActive {
					update := repo.EXPECT().FinishActive(txCtx, id, at).Return(completed, tt.updated, tt.updateErr).After(read)
					if tt.updateErr == nil && tt.updated {
						from := domain.StatusActive
						repo.EXPECT().AddStatusChange(txCtx, id, &from, domain.StatusCompleted, "finished by driver").
							Return(tt.historyErr).After(update)
					}
				}
			}
			got, err := finish.New(tx, repo, func() time.Time { return now }).FinishTrip(ctx, id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			want := completed
			if tt.wantErr != nil {
				want = domain.Trip{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("trip = %#v, want %#v", got, want)
			}
		})
	}
}
