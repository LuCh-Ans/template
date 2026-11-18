package get_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/get"
)

func TestGetTrip(t *testing.T) {
	id := uuid.New()
	tests := []struct {
		name string
		trip domain.Trip
		err  error
	}{
		{name: "found", trip: domain.Trip{ID: id, Status: domain.StatusActive}},
		{name: "missing", err: domain.ErrTripNotFound},
		{name: "repository failure", err: errors.New("read failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewMocktripRepository(gomock.NewController(t))
			ctx := t.Context()
			repo.EXPECT().GetByID(ctx, id).Return(tt.trip, tt.err)
			got, err := get.New(repo).GetTrip(ctx, id)
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if !reflect.DeepEqual(got, tt.trip) {
				t.Errorf("trip = %#v, want %#v", got, tt.trip)
			}
		})
	}
}
