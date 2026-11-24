//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=saveposition_test

package saveposition

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type positionRepository interface {
	UpdateLastPosition(ctx context.Context, id uuid.UUID, at time.Time) error
	InsertPosition(ctx context.Context, position domain.Position) (domain.Position, error)
}

type txManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type positionTasks interface {
	Cancel(tripID uuid.UUID)
}
