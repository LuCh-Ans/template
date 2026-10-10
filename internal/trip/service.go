package trip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/LuCh-Ans/template/internal/domain"
	"github.com/LuCh-Ans/template/internal/postgres"
	"github.com/google/uuid"
)

type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Сервис поездок
type Service struct {
	tx             TxManager // атомарность
	repo           *postgres.TripRepository
	now            func() time.Time
	idempotencyTTL time.Duration
}

func NewService(tx TxManager, repo *postgres.TripRepository, idempotencyTTL time.Duration) *Service {
	return &Service{
		tx:             tx,
		repo:           repo,
		now:            func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
		idempotencyTTL: idempotencyTTL,
	}
}

type CreateTripInput struct {
	UserID         uuid.UUID
	DriverID       uuid.UUID
	Start          domain.Point
	End            domain.Point
	Price          int64
	IdempotencyKey *uuid.UUID
}

// Создаёт поездку, created = false это повтор запроса с тем же Idempotency-Key
// возвращается поездка, созданная первым запросом
func (s *Service) CreateTrip(ctx context.Context, in CreateTripInput) (domain.Trip, bool, error) {
	newTrip := domain.Trip{
		ID:        uuid.New(),
		UserID:    in.UserID,
		DriverID:  in.DriverID,
		Start:     in.Start,
		End:       in.End,
		Price:     in.Price,
		Status:    domain.StatusActive,
		StartedAt: s.now(),
	}

	var (
		result  domain.Trip
		created bool
	)
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if in.IdempotencyKey != nil {
			hash := requestHash(in)
			// Сначала занимаем ключ, параллельный запрос с тем же ключом будет ждать здесь
			claimed, err := s.repo.ClaimIdempotencyKey(ctx, *in.IdempotencyKey, hash, newTrip.ID, s.idempotencyTTL)
			if err != nil {
				return err
			}
			if !claimed {
				existing, err := s.replay(ctx, *in.IdempotencyKey, hash)
				if err != nil {
					return err
				}
				result = existing
				return nil
			}
		}

		if err := s.repo.Create(ctx, newTrip); err != nil {
			return err
		}
		if err := s.repo.AddStatusChange(ctx, newTrip.ID, nil, domain.StatusActive, "trip created"); err != nil {
			return err
		}
		result, created = newTrip, true
		return nil
	})
	if err != nil {
		return domain.Trip{}, false, fmt.Errorf("create trip: %w", err)
	}
	return result, created, nil
}

// Ключ уже занят, если тело то же - возвращаем ранее созданную поездку, другое - конфликт
func (s *Service) replay(ctx context.Context, key uuid.UUID, hash string) (domain.Trip, error) {
	storedHash, tripID, err := s.repo.GetIdempotencyKey(ctx, key)
	if err != nil {
		return domain.Trip{}, err
	}
	if storedHash != hash {
		return domain.Trip{}, domain.ErrIdempotencyConflict // 409
	}
	return s.repo.GetByID(ctx, tripID)
}

// Отпечаток содержимого запроса по уже разобранным полям: пробелы, порядок ключей
// и регистр UUID в исходном JSON на него не влияют
func requestHash(in CreateTripInput) string {
	payload := fmt.Sprintf("%s|%s|%v|%v|%v|%v|%d",
		in.UserID, in.DriverID,
		in.Start.Latitude, in.Start.Longitude, in.End.Latitude, in.End.Longitude,
		in.Price,
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
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
