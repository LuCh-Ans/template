//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=get_test

package get

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type tripRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error)
}
