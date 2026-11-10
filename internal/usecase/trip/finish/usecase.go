package finish

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type UseCase struct {
	logger  *slog.Logger
	metrics tripMetrics
	tx      txManager
	repo    tripRepository
	now     func() time.Time
}

func New(
	logger *slog.Logger,
	metrics tripMetrics,
	tx txManager,
	repo tripRepository,
	now func() time.Time,
) *UseCase {
	return &UseCase{
		logger:  logger,
		metrics: metrics,
		tx:      tx,
		repo:    repo,
		now:     now,
	}
}

func (u *UseCase) FinishTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	var finished domain.Trip

	err := u.tx.Do(ctx, func(ctx context.Context) error {
		current, err := u.repo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		at := u.now().UTC().Truncate(time.Microsecond)
		if err := current.Finish(at); err != nil {
			return err
		}

		trip, updated, err := u.repo.FinishActive(ctx, id, at)
		if err != nil {
			return err
		}
		if !updated {
			return domain.ErrTripCompleted
		}

		from := domain.StatusActive
		if err := u.repo.AddStatusChange(
			ctx, id, &from, domain.StatusCompleted, "finished by driver",
		); err != nil {
			return err
		}

		finished = trip
		return nil
	})
	if err != nil {
		return domain.Trip{}, fmt.Errorf("finish trip: %w", err)
	}
	u.metrics.TripCompleted(ctx)
	u.logger.InfoContext(ctx, "trip completed", "trip_id", finished.ID.String())
	return finished, nil
}
