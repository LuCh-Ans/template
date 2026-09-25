package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// txKey - ключ, под которым транзакция лежит в context
type txKey struct{}

type TxManager struct {
	pool     *pgxpool.Pool
	isoLevel pgx.TxIsoLevel
}

func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{
		pool:     pool,
		isoLevel: pgx.ReadCommitted,
	}
}

// Do выполняет fn в транзакции, fn вернула nil -COMMIT, вернула ошибку или паникнула - ROLLBACK
// Если транзакция уже есть в ctx, новая не открывается
func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: m.isoLevel})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// executor возвращает транзакцию из контекста, если она есть, иначе пул
func (m *TxManager) executor(ctx context.Context) Executor {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return m.pool
}
