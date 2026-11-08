package create

import (
	"context"
	"crypto/sha256" // хэш для идемпотентности
	"encoding/hex"  // хэш - строка из 64 символов
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

type UseCase struct {
	logger         *slog.Logger
	metrics        tripMetrics
	tx             txManager
	repo           tripRepository
	now            func() time.Time
	idempotencyTTL time.Duration
}

func New(
	logger *slog.Logger,
	metrics tripMetrics,
	tx txManager,
	repo tripRepository,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *UseCase {
	return &UseCase{
		logger:         logger,
		metrics:        metrics,
		tx:             tx,
		repo:           repo,
		now:            now,
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
func (s *UseCase) CreateTrip(ctx context.Context, in CreateTripInput) (domain.Trip, bool, error) {
	newTrip := domain.Trip{
		ID:        uuid.New(), // придумаывем сами в го
		UserID:    in.UserID,
		DriverID:  in.DriverID,
		Start:     in.Start,
		End:       in.End,
		Price:     in.Price,
		Status:    domain.StatusActive,
		StartedAt: s.now().UTC().Truncate(time.Microsecond),
	}

	if err := newTrip.Validate(); err != nil {
		return domain.Trip{}, false, fmt.Errorf("validate trip: %w", err)
	}

	var (
		result  domain.Trip
		created bool
	)
	// Вход в транзакцию
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
	if created {
		s.metrics.TripCreated(ctx)
		s.logger.InfoContext(ctx, "trip created", "trip_id", result.ID.String())
	}
	return result, created, nil
}

// Ключ уже занят, если тело то же - возвращаем ранее созданную поездку, другое - конфликт
func (s *UseCase) replay(ctx context.Context, key uuid.UUID, hash string) (domain.Trip, error) {
	storedHash, tripID, err := s.repo.GetIdempotencyKey(ctx, key) // что сохренил первый запрос
	if err != nil {
		return domain.Trip{}, err
	}
	if storedHash != hash {
		return domain.Trip{}, domain.ErrIdempotencyConflict // тот же ключ, но другое тело 409
	}
	return s.repo.GetByID(ctx, tripID) // та же поездка
}

// Хэш содержимого запроса по уже разобранным полям: пробелы, порядок ключей
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
