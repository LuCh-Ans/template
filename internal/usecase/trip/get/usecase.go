package get

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type UseCase struct {
	repo tripRepository
}

func New(repo tripRepository) *UseCase {
	return &UseCase{repo: repo}
}

func (u *UseCase) GetTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	trip, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Trip{}, fmt.Errorf("get trip: %w", err)
	}
	return trip, nil
}
