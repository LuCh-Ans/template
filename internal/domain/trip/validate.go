package trip

import (
	"math"

	"github.com/google/uuid"
)

func (t Trip) Validate() error {
	if t.UserID == uuid.Nil {
		return &ValidationError{
			Field:   "user_id",
			Message: "must not be empty",
		}
	}
	if t.DriverID == uuid.Nil {
		return &ValidationError{
			Field:   "driver_id",
			Message: "must not be empty",
		}
	}
	if err := validatePoint("start_point", t.Start); err != nil {
		return err
	}
	if err := validatePoint("end_point", t.End); err != nil {
		return err
	}
	if t.Price < 0 {
		return &ValidationError{
			Field:   "price",
			Message: "must be >= 0",
		}
	}
	return nil
}

func validatePoint(field string, point Point) error {
	if math.IsNaN(point.Latitude) || point.Latitude < -90 || point.Latitude > 90 {
		return &ValidationError{
			Field:   field + ".latitude",
			Message: "must be in [-90, 90]",
		}
	}
	if math.IsNaN(point.Longitude) || point.Longitude < -180 || point.Longitude > 180 {
		return &ValidationError{
			Field:   field + ".longitude",
			Message: "must be in [-180, 180]",
		}
	}
	return nil
}
