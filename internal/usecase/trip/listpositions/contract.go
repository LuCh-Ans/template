//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=listpositions_test

package listpositions

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type positionRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error)
	ListPositions(ctx context.Context, id uuid.UUID) ([]domain.Position, error)
}
