package saveposition

import (
	"context"
	"fmt"
	"time"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type UseCase struct {
	tx   txManager
	repo positionRepository
	now  func() time.Time
}

func New(tx txManager, repo positionRepository, now func() time.Time) *UseCase {
	return &UseCase{tx: tx, repo: repo, now: now}
}

func (u *UseCase) SavePosition(ctx context.Context, position domain.Position) (domain.Position, error) {
	if err := position.Validate(u.now()); err != nil {
		return domain.Position{}, fmt.Errorf("validate position: %w", err)
	}

	var saved domain.Position
	err := u.tx.Do(ctx, func(ctx context.Context) error {
		if err := u.repo.UpdateLastPosition(ctx, position.TripID, position.RecordedAt); err != nil {
			return err
		}
		var err error
		saved, err = u.repo.InsertPosition(ctx, position)
		return err
	})
	if err != nil {
		return domain.Position{}, fmt.Errorf("save position: %w", err)
	}
	return saved, nil
}
