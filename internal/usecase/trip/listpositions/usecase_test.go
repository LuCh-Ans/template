package listpositions_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/listpositions"
)

func TestListPositions(t *testing.T) {
	failure := errors.New("positions unavailable")
	id := uuid.New()
	point := domain.Position{ID: 7, TripID: id, RecordedAt: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)}
	for _, tt := range []struct {
		name            string
		positions       []domain.Position
		getErr, listErr error
	}{
		{name: "empty route", positions: []domain.Position{}},
		{name: "route with points", positions: []domain.Position{point}},
		{name: "missing trip", getErr: domain.ErrTripNotFound},
		{name: "trip read fails", getErr: failure},
		{name: "positions read fails", listErr: failure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewMockpositionRepository(gomock.NewController(t))
			read := repo.EXPECT().GetByID(t.Context(), id).Return(domain.Trip{ID: id}, tt.getErr)
			if tt.getErr == nil {
				repo.EXPECT().ListPositions(t.Context(), id).Return(tt.positions, tt.listErr).After(read)
			}
			got, err := listpositions.New(repo).ListPositions(t.Context(), id)
			wantErr := tt.getErr
			if wantErr == nil {
				wantErr = tt.listErr
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("error=%v, want %v", err, wantErr)
			}
			want := tt.positions
			if wantErr != nil {
				want = nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("positions=%+v, want %+v", got, want)
			}
		})
	}
}
