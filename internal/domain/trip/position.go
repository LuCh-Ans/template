package trip

import (
	"time"

	"github.com/google/uuid"
)

type Position struct {
	ID          int64
	TripID      uuid.UUID
	Coordinates Point
	RecordedAt  time.Time
}

func (p Position) Validate(now time.Time) error {
	if err := validatePoint("coordinates", p.Coordinates); err != nil {
		return err
	}
	if p.RecordedAt.IsZero() {
		return &ValidationError{Field: "recorded_at", Message: "is required"}
	}
	if p.RecordedAt.After(now.Add(time.Minute)) {
		return &ValidationError{Field: "recorded_at", Message: "must not be more than one minute in the future"}
	}
	return nil
}
