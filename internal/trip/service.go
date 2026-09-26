package trip

import (
	"context"
	"fmt"
	"time"

	"github.com/LuCh-Ans/template/internal/domain"
	"github.com/LuCh-Ans/template/internal/postgres"
	"github.com/google/uuid"
)

type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type Service struct {
	tx   TxManager
	repo *postgres.TripRepository
	now  func() time.Time
}

func NewService(tx TxManager, repo *postgres.TripRepository) *Service {
	return &Service{
		tx:   tx,
		repo: repo,
		now:  func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}
}

type CreateTripInput struct {
	UserID   uuid.UUID
	DriverID uuid.UUID
	Start    domain.Point
	End      domain.Point
	Price    int64
}

func (s *Service) CreateTrip(ctx context.Context, in CreateTripInput) (domain.Trip, error) {
	trip := domain.Trip{
		ID:        uuid.New(),
		UserID:    in.UserID,
		DriverID:  in.DriverID,
		Start:     in.Start,
		End:       in.End,
		Price:     in.Price,
		Status:    domain.StatusActive,
		StartedAt: s.now(),
	}

	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if err := s.repo.Create(ctx, trip); err != nil {
			return err
		}
		return s.repo.AddStatusChange(ctx, trip.ID, nil, domain.StatusActive, "trip created")
	})
	if err != nil {
		return domain.Trip{}, fmt.Errorf("create trip: %w", err)
	}
	return trip, nil
}

func (s *Service) GetTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	trip, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Trip{}, fmt.Errorf("get trip: %w", err)
	}
	return trip, nil
}

func (s *Service) FinishTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	var finished domain.Trip

	err := s.tx.Do(ctx, func(ctx context.Context) error {
		trip, updated, err := s.repo.FinishActive(ctx, id, s.now())
		if err != nil {
			return err
		}

		if !updated {
			if _, err := s.repo.GetByID(ctx, id); err != nil {
				return err // ErrTripNotFound 404
			}
			return domain.ErrTripCompleted // 409
		}

		from := domain.StatusActive
		if err := s.repo.AddStatusChange(ctx, id, &from, domain.StatusCompleted, "finished by driver"); err != nil {
			return err
		}

		finished = trip
		return nil
	})
	if err != nil {
		return domain.Trip{}, fmt.Errorf("finish trip: %w", err)
	}
	return finished, nil
}
