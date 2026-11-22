package trip_test

import (
	"errors"
	"math"
	"testing"
	"time"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

func TestPositionValidate(t *testing.T) {
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		point      domain.Point
		recordedAt time.Time
		field      string
	}{
		{name: "one minute ahead", point: domain.Point{Latitude: -90, Longitude: 180}, recordedAt: now.Add(time.Minute)},
		{name: "over one minute ahead", recordedAt: now.Add(time.Minute + time.Nanosecond), field: "recorded_at"},
		{name: "missing timestamp", field: "recorded_at"},
		{name: "invalid latitude", point: domain.Point{Latitude: 90.1}, recordedAt: now, field: "coordinates.latitude"},
		{name: "invalid longitude", point: domain.Point{Longitude: math.NaN()}, recordedAt: now, field: "coordinates.longitude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (domain.Position{Coordinates: tc.point, RecordedAt: tc.recordedAt}).Validate(now)
			if tc.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var validation *domain.ValidationError
			if !errors.As(err, &validation) || validation.Field != tc.field {
				t.Fatalf("error = %v, want invalid %s", err, tc.field)
			}
		})
	}
}
