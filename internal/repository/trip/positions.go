package trip

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
)

// Обновляет только активную поездку и удерживает блокировку строки до конца транзакции
func (r *TripRepository) UpdateLastPosition(ctx context.Context, id uuid.UUID, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	executor := r.tx.Executor(ctx)
	tag, err := executor.Exec(ctx, `
  UPDATE trips
  SET last_position_at = GREATEST(last_position_at, $2), updated_at = now()
  WHERE id = $1 AND status = $3`, id, at, domain.StatusActive)
	if err != nil {
		return fmt.Errorf("update last position: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var exists int
	err = executor.QueryRow(ctx, "SELECT 1 FROM trips WHERE id = $1", id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrTripNotFound
	}
	if err != nil {
		return fmt.Errorf("check trip existence: %w", err)
	}
	return domain.ErrTripCompleted
}

func (r *TripRepository) InsertPosition(ctx context.Context, position domain.Position) (domain.Position, error) {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	err := r.tx.Executor(ctx).QueryRow(ctx, `
  INSERT INTO trip_positions (trip_id, latitude, longitude, recorded_at)
  VALUES ($1, $2, $3, $4)
  RETURNING id, recorded_at`, position.TripID, position.Coordinates.Latitude,
		position.Coordinates.Longitude, position.RecordedAt,
	).Scan(&position.ID, &position.RecordedAt)
	if err != nil {
		return domain.Position{}, fmt.Errorf("insert position: %w", err)
	}
	position.RecordedAt = position.RecordedAt.UTC()
	return position, nil
}

func (r *TripRepository) ListPositions(ctx context.Context, id uuid.UUID) ([]domain.Position, error) {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	rows, err := r.tx.Executor(ctx).Query(ctx, `
		SELECT id, trip_id, latitude, longitude, recorded_at
		FROM trip_positions
		WHERE trip_id = $1
		ORDER BY recorded_at ASC, id ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("query trip positions: %w", err)
	}
	defer rows.Close()

	positions := make([]domain.Position, 0)
	for rows.Next() {
		var p domain.Position
		if err := rows.Scan(
			&p.ID, &p.TripID,
			&p.Coordinates.Latitude, &p.Coordinates.Longitude,
			&p.RecordedAt,
		); err != nil {
			return nil, fmt.Errorf("scan trip position: %w", err)
		}
		p.RecordedAt = p.RecordedAt.UTC()
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read trip positions: %w", err)
	}
	return positions, nil
}
