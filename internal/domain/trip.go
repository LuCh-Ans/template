package domain

import (
	"errors"
	"time"
	"github.com/google/uuid"
)

type TripStatus string

const (
	StatusActive TripStatus = "active"
	StatusCompleted TripStatus = "completed"
)

type Point struct {
	Latitude float64
	Longitude float64
}

type Trip struct {
	ID uuid.UUID
	UserID uuid.UUID
	DriverID uuid.UUID
	Start Point
	End Point
	Price int64 
	Status TripStatus
	StartedAt time.Time
	FinishedAt *time.Time
}

var (
	ErrTripNotFound = errors.New("trip not found")
	ErrTripCompleted = errors.New("trip already completed")
	ErrDriverBusy = errors.New("driver already has an active trip")
)