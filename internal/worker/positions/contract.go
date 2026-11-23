package positions

import (
	"context"
	"time"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=positions

type staleTripRepository interface {
	ListStaleTrips(ctx context.Context, staleAfter time.Duration) ([]domain.StaleTrip, error)
}

// Ticker позволяет управлять тиками в тестах без ожидания реального времени.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}
