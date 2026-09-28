package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Общий интерфейс для пула и транзакции
type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Ключ для контекста
type txKey struct{}

// Менеджер 
type TxManager struct {
	pool *pgxpool.Pool
	isoLevel pgx.TxIsoLevel
	timeout  time.Duration // лимит на BEGIN, COMMIT и ROLLBACK
}

func NewTxManager(pool *pgxpool.Pool, timeout time.Duration) *TxManager {
	return &TxManager{
		pool: pool,
		isoLevel: pgx.ReadCommitted,
		timeout:  timeout,
	}
}

// Do выполняет функцию fn в транзакции, fn вернула nil COMMIT, вернула ошибку или паникнула ROLLBACK
// Если транзакция уже есть, новая не открывается
func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	// Ищет значение по ключу
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	beginCtx, cancelBegin := context.WithTimeout(ctx, m.timeout)
	tx, err := m.pool.BeginTx(beginCtx, pgx.TxOptions{IsoLevel: m.isoLevel})
	cancelBegin()

	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	// Защита от паники
	defer func() {
		if p := recover(); p != nil {
			_ = m.rollback(ctx, tx)
			panic(p)
		}
	}()
	
	// Создает новый контекст
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		if rbErr := m.rollback(ctx, tx); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}

	commitCtx, cancelCommit := context.WithTimeout(ctx, m.timeout)
	defer cancelCommit()

	if err := tx.Commit(commitCtx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Откатывает транзакцию, даже если контекст запроса уже отменён
func (m *TxManager) rollback(ctx context.Context, tx pgx.Tx) error {
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.timeout)
	defer cancel()

	if err := tx.Rollback(rbCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return err
	}
	return nil
}

// Возвращает транзакцию из контекста, если она есть, иначе пул
func (m *TxManager) executor(ctx context.Context) Executor {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return m.pool
}
