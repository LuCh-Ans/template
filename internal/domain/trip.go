package domain

import (
	"errors"
	"time"
	"github.com/google/uuid"
)

// Тип для статуса проверок
type TripStatus string

const (
	StatusActive TripStatus = "active"
	StatusCompleted TripStatus = "completed"
)

// Точка на карте
type Point struct {
	Latitude float64
	Longitude float64
}

// Поездка
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

// Доменные ошибки
var (
	ErrTripNotFound = errors.New("trip not found")
	ErrTripCompleted = errors.New("trip already completed")
	ErrDriverBusy = errors.New("driver already has an active trip")
)