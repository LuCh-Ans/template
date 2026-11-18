package trip

import (
	"time"

	"github.com/google/uuid"
)

// Тип для статуса проверок
type TripStatus string

const (
	StatusActive    TripStatus = "active"
	StatusCompleted TripStatus = "completed"
)

// Точка на карте
type Point struct {
	Latitude  float64
	Longitude float64
}

// Поездка
type Trip struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	DriverID   uuid.UUID
	Start      Point
	End        Point
	Price      int64
	Status     TripStatus
	StartedAt  time.Time
	FinishedAt *time.Time
}

// Завершает активную поездку в момент at
func (t *Trip) Finish(at time.Time) error {
	if t.Status != StatusActive {
		return ErrTripCompleted
	}

	t.Status = StatusCompleted
	t.FinishedAt = &at
	return nil
}
