package trip_test

import (
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

func TestTripValidate(t *testing.T) {
	const (
		startLatitudeField = "start_point.latitude"
		latitudeMessage    = "must be in [-90, 90]"
		longitudeMessage   = "must be in [-180, 180]"
		endLongitudeField  = "end_point.longitude"
	)

	tests := []struct {
		name    string
		change  func(*domain.Trip)
		field   string
		message string
	}{
		{name: "valid zero price and coordinates"},
		{name: "coordinate boundaries", change: func(trip *domain.Trip) {
			trip.Start = domain.Point{Latitude: -90, Longitude: -180}
			trip.End = domain.Point{Latitude: 90, Longitude: 180}
		}},
		{name: "missing user", change: func(trip *domain.Trip) { trip.UserID = uuid.Nil }, field: "user_id", message: "must not be empty"},
		{name: "missing driver", change: func(trip *domain.Trip) { trip.DriverID = uuid.Nil }, field: "driver_id", message: "must not be empty"},
		{name: "negative price", change: func(trip *domain.Trip) { trip.Price = -1 }, field: "price", message: "must be >= 0"},
		{name: "start latitude below range", change: func(trip *domain.Trip) { trip.Start.Latitude = -90.01 }, field: startLatitudeField, message: latitudeMessage},
		{name: "start latitude above range", change: func(trip *domain.Trip) { trip.Start.Latitude = 90.01 }, field: startLatitudeField, message: latitudeMessage},
		{name: "start longitude below range", change: func(trip *domain.Trip) { trip.Start.Longitude = -180.01 }, field: "start_point.longitude", message: longitudeMessage},
		{name: "end longitude above range", change: func(trip *domain.Trip) { trip.End.Longitude = 180.01 }, field: endLongitudeField, message: longitudeMessage},
		{name: "end latitude is NaN", change: func(trip *domain.Trip) { trip.End.Latitude = math.NaN() }, field: "end_point.latitude", message: latitudeMessage},
		{name: "end longitude is NaN", change: func(trip *domain.Trip) { trip.End.Longitude = math.NaN() }, field: endLongitudeField, message: longitudeMessage},
		{name: "infinite latitude", change: func(trip *domain.Trip) { trip.Start.Latitude = math.Inf(1) }, field: startLatitudeField, message: latitudeMessage},
		{name: "infinite longitude", change: func(trip *domain.Trip) { trip.End.Longitude = math.Inf(-1) }, field: endLongitudeField, message: longitudeMessage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trip := domain.Trip{UserID: uuid.New(), DriverID: uuid.New()}
			if tt.change != nil {
				tt.change(&trip)
			}
			err := trip.Validate()
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			var validationErr *domain.ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Validate() = %v, want ValidationError", err)
			}
			if validationErr.Field != tt.field || validationErr.Message != tt.message {
				t.Fatalf("ValidationError = %#v, want %s / %s", validationErr, tt.field, tt.message)
			}
			if got, want := err.Error(), tt.field+" "+tt.message; got != want {
				t.Errorf("Error() = %q, want %q", got, want)
			}
		})
	}
}
