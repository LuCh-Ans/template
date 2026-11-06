//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=create_test

package create

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type tripRepository interface {
	Create(ctx context.Context, trip domain.Trip) error
	GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error)
	AddStatusChange(ctx context.Context, tripID uuid.UUID, from *domain.TripStatus, to domain.TripStatus, reason string) error
	ClaimIdempotencyKey(ctx context.Context, key uuid.UUID, requestHash string, tripID uuid.UUID, ttl time.Duration) (bool, error)
	GetIdempotencyKey(ctx context.Context, key uuid.UUID) (string, uuid.UUID, error)
}

type txManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
