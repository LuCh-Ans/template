//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=finish_test

package finish

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type tripRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error)
	FinishActive(ctx context.Context, id uuid.UUID, at time.Time) (domain.Trip, bool, error)
	AddStatusChange(
		ctx context.Context,
		tripID uuid.UUID,
		from *domain.TripStatus,
		to domain.TripStatus,
		reason string,
	) error
}

type txManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
