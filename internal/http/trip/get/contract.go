package get

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type tripUseCase interface {
	GetTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error)
}
