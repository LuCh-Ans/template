package trip_test

import (
	"errors"
	"testing"
	"time"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

func TestTripFinish(t *testing.T) {
	first := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	later := first.Add(time.Hour)

	tests := []struct {
		name    string
		trip    domain.Trip
		at      time.Time
		wantAt  time.Time
		wantErr error
	}{
		{
			name:   "active trip is completed",
			trip:   domain.Trip{Status: domain.StatusActive},
			at:     first,
			wantAt: first,
		},
		{
			name: "completed trip keeps its original finish time",
			trip: domain.Trip{
				Status:     domain.StatusCompleted,
				FinishedAt: &first,
			},
			at:      later,
			wantAt:  first,
			wantErr: domain.ErrTripCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.trip.Finish(tt.at)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Finish() error = %v, want %v", err, tt.wantErr)
			}
			if tt.trip.Status != domain.StatusCompleted {
				t.Errorf("Status = %q, want %q", tt.trip.Status, domain.StatusCompleted)
			}
			if tt.trip.FinishedAt == nil {
				t.Fatal("FinishedAt is nil")
			}
			if !tt.trip.FinishedAt.Equal(tt.wantAt) {
				t.Errorf("FinishedAt = %v, want %v", tt.trip.FinishedAt, tt.wantAt)
			}
		})
	}
}
