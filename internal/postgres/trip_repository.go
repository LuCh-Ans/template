package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LuCh-Ans/template/internal/domain"
	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	pgUniqueViolation      = "23505"
	driverActiveConstraint = "trips_driver_active_uniq"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

var tripColumns = []string{
	"id", "user_id", "driver_id",
	"start_latitude", "start_longitude", "end_latitude", "end_longitude",
	"price", "status", "started_at", "finished_at",
}

type TripRepository struct {
	tx           *TxManager
	queryTimeout time.Duration
}

func NewTripRepository(tx *TxManager, queryTimeout time.Duration) *TripRepository {
	return &TripRepository{tx: tx, queryTimeout: queryTimeout}
}

func (r *TripRepository) Create(ctx context.Context, t domain.Trip) error {
	query, args, err := psql.Insert("trips").
		Columns(tripColumns...).
		Values(
			t.ID, t.UserID, t.DriverID,
			t.Start.Latitude, t.Start.Longitude, t.End.Latitude, t.End.Longitude,
			t.Price, string(t.Status), t.StartedAt, t.FinishedAt,
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert trip: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	if _, err := r.tx.executor(ctx).Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) &&
			pgErr.Code == pgUniqueViolation &&
			pgErr.ConstraintName == driverActiveConstraint {
			return domain.ErrDriverBusy
		}
		return fmt.Errorf("insert trip: %w", err)
	}
	return nil
}

func (r *TripRepository) AddStatusChange(
	ctx context.Context,
	tripID uuid.UUID,
	from *domain.TripStatus,
	to domain.TripStatus,
	reason string,
) error {
	var fromValue any
	if from != nil {
		fromValue = string(*from)
	}

	query, args, err := psql.Insert("trip_status_history").
		Columns("trip_id", "from_status", "to_status", "reason").
		Values(tripID, fromValue, string(to), reason).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert status history: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	if _, err := r.tx.executor(ctx).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("insert status history: %w", err)
	}
	return nil
}

func (r *TripRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	query, args, err := psql.Select(tripColumns...).
		From("trips").
		Where(sq.Eq{"id": id}).
		ToSql()
	if err != nil {
		return domain.Trip{}, fmt.Errorf("build select trip: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	trip, err := scanTrip(r.tx.executor(ctx).QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, domain.ErrTripNotFound
	}
	if err != nil {
		return domain.Trip{}, fmt.Errorf("select trip: %w", err)
	}
	return trip, nil
}

// FinishActive переводит активную поездку в completed одним UPDATE
// updated == false: активной поездки с таким id нет — либо её нет вообще, либо она уже завершена
func (r *TripRepository) FinishActive(ctx context.Context, id uuid.UUID, finishedAt time.Time) (trip domain.Trip, updated bool, err error) {
	query, args, err := psql.Update("trips").
		Set("status", string(domain.StatusCompleted)).
		Set("finished_at", finishedAt).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id, "status": string(domain.StatusActive)}).
		Suffix("RETURNING " + strings.Join(tripColumns, ", ")).
		ToSql()
	if err != nil {
		return domain.Trip{}, false, fmt.Errorf("build finish trip: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	trip, err = scanTrip(r.tx.executor(ctx).QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, false, nil
	}
	if err != nil {
		return domain.Trip{}, false, fmt.Errorf("finish trip: %w", err)
	}
	return trip, true, nil
}

func scanTrip(row pgx.Row) (domain.Trip, error) {
	var (
		t domain.Trip
		status string
	)
	err := row.Scan(
		&t.ID, &t.UserID, &t.DriverID,
		&t.Start.Latitude, &t.Start.Longitude, &t.End.Latitude, &t.End.Longitude,
		&t.Price, &status, &t.StartedAt, &t.FinishedAt,
	)
	t.Status = domain.TripStatus(status)
	return t, err
}
