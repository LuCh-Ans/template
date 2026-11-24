package positions

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=positions

type staleTripRepository interface {
	ListStaleTrips(ctx context.Context, staleAfter time.Duration) ([]domain.StaleTrip, error)
}

// Реализация гейтвея сопоставляет аргументы HTTP-контракту Push Service:
// recipient_id = driverID, kind = REQUEST_POSITION, data.trip_id = tripID.
type positionRequester interface {
	RequestPosition(ctx context.Context, tripID, driverID uuid.UUID, requestID string) error
}

type scannerRunner interface {
	Run(ctx context.Context, ticker Ticker)
}

type poolRunner interface {
	Run(ctx context.Context) error
}

// Ticker позволяет управлять тиками в тестах без ожидания реального времени.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}
