package listpositions

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type UseCase struct {
	repo positionRepository
}

func New(repo positionRepository) *UseCase {
	return &UseCase{repo: repo}
}

func (u *UseCase) ListPositions(ctx context.Context, id uuid.UUID) ([]domain.Position, error) {
	if _, err := u.repo.GetByID(ctx, id); err != nil {
		return nil, fmt.Errorf("get trip for route: %w", err)
	}

	positions, err := u.repo.ListPositions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list trip positions: %w", err)
	}
	return positions, nil
}
