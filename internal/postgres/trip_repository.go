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

// Коды ошибок postgres
const (
	pgUniqueViolation      = "23505"
	driverActiveConstraint = "trips_driver_active_uniq"
)

// Билдер squirrel
var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Колонки поездки
var tripColumns = []string{
	"id", "user_id", "driver_id",
	"start_latitude", "start_longitude", "end_latitude", "end_longitude",
	"price", "status", "started_at", "finished_at",
}

// Репозиторий поездок
type TripRepository struct {
	tx           *TxManager
	queryTimeout time.Duration
}

func NewTripRepository(tx *TxManager, queryTimeout time.Duration) *TripRepository {
	return &TripRepository{tx: tx, queryTimeout: queryTimeout}
}

// Вставка поездки
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

// Запись в журнал
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

// Найти поездку по id
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

// Переводит активную поездку в completed одним UPDATE
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

// Пытается занять ключ идемпотентности под поездку tripID
// true, если ключ новый или его срок истёк, теперь указывает на tripID
// false, если ключ уже занят живой записью, это повтор или конфликт
func (r *TripRepository) ClaimIdempotencyKey(ctx context.Context, key uuid.UUID, requestHash string, tripID uuid.UUID, ttl time.Duration) (bool, error) {
	query, args, err := psql.Insert("idempotency_keys").
		Columns("key", "request_hash", "trip_id").
		Values(key, requestHash, tripID).
		Suffix("ON CONFLICT (key) DO UPDATE"+
			" SET request_hash = EXCLUDED.request_hash, trip_id = EXCLUDED.trip_id, created_at = now()"+
			" WHERE idempotency_keys.created_at < now() - make_interval(secs => ?)", ttl.Seconds()).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("build claim idempotency key: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	tag, err := r.tx.executor(ctx).Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("claim idempotency key: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Возвращает хэш запроса и айдишник поездки, сохранённые под ключом
func (r *TripRepository) GetIdempotencyKey(ctx context.Context, key uuid.UUID) (requestHash string, tripID uuid.UUID, err error) {
	query, args, err := psql.Select("request_hash", "trip_id").
		From("idempotency_keys").
		Where(sq.Eq{"key": key}).
		ToSql()
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("build select idempotency key: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	if err := r.tx.executor(ctx).QueryRow(ctx, query, args...).Scan(&requestHash, &tripID); err != nil {
		return "", uuid.Nil, fmt.Errorf("select idempotency key: %w", err)
	}
	return requestHash, tripID, nil
}

func scanTrip(row pgx.Row) (domain.Trip, error) {
	var (
		t      domain.Trip
		status string
	)
	err := row.Scan(
		&t.ID, &t.UserID, &t.DriverID,
		&t.Start.Latitude, &t.Start.Longitude, &t.End.Latitude, &t.End.Longitude,
		&t.Price, &status, &t.StartedAt, &t.FinishedAt,
	)
	t.Status = domain.TripStatus(status)

	// pgx отдаёт TIMESTAMPTZ в часовом поясе машины, приводим к UTC
	t.StartedAt = t.StartedAt.UTC()
	if t.FinishedAt != nil {
		finished := t.FinishedAt.UTC()
		t.FinishedAt = &finished
	}
	return t, err
}
