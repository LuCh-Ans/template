package finish

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type tripUseCase interface {
	FinishTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error)
}
