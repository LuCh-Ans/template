package container

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LuCh-Ans/template/internal/config"
	httpapi "github.com/LuCh-Ans/template/internal/http"
	"github.com/LuCh-Ans/template/internal/http/response"
	createhttp "github.com/LuCh-Ans/template/internal/http/trip/create"
	finishhttp "github.com/LuCh-Ans/template/internal/http/trip/finish"
	gethttp "github.com/LuCh-Ans/template/internal/http/trip/get"
	"github.com/LuCh-Ans/template/internal/infra/postgres"
	"github.com/LuCh-Ans/template/internal/infra/postgres/transaction"
	"github.com/LuCh-Ans/template/internal/infra/telemetry"
	triprepo "github.com/LuCh-Ans/template/internal/repository/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/create"
	"github.com/LuCh-Ans/template/internal/usecase/trip/finish"
	"github.com/LuCh-Ans/template/internal/usecase/trip/get"
)

type Container struct {
	Server            *http.Server
	Logger            *slog.Logger
	pool              *pgxpool.Pool
	shutdownTelemetry func(context.Context) error
	tripMetrics       *telemetry.TripMetrics
}

func New(ctx context.Context, cfg config.Config) (_ *Container, err error) {
	logger := telemetry.NewLogger(
		os.Stdout,
		cfg.LogLevel,
		cfg.Telemetry.ServiceName,
	)

	// SDK должен существовать до создания пула и инструментов.
	shutdownTelemetry, err := telemetry.Setup(ctx, cfg.Telemetry, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}

	app := &Container{
		Logger:            logger,
		shutdownTelemetry: shutdownTelemetry,
	}

	// При ошибке сборки закрываем всё, что успели создать
	defer func() {
		if err == nil {
			return
		}

		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			cfg.ShutdownTimeout,
		)
		defer cancel()

		err = errors.Join(err, app.Close(cleanupCtx))
	}()

	pool, err := postgres.NewPool(ctx, cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("initialize postgres: %w", err)
	}
	app.pool = pool
	logger.Info("connected to database")

	txManager := transaction.NewTxManager(pool, cfg.DB.QueryTimeout)
	tripRepo := triprepo.NewTripRepository(txManager, cfg.DB.QueryTimeout)

	tripMetrics, err := telemetry.NewTripMetrics(tripRepo)
	if err != nil {
		return nil, fmt.Errorf("initialize trip metrics: %w", err)
	}
	app.tripMetrics = tripMetrics

	createTrip := create.New(
		logger,
		tripMetrics,
		txManager,
		tripRepo,
		cfg.IdempotencyTTL,
		time.Now,
	)
	getTrip := get.New(tripRepo)
	finishTrip := finish.New(
		logger,
		tripMetrics,
		txManager,
		tripRepo,
		time.Now,
	)

	responses := response.New(logger)

	createHandler := createhttp.New(createTrip, responses)
	getHandler := gethttp.New(getTrip, responses)
	finishHandler := finishhttp.New(finishTrip, responses)

	handler := httpapi.NewHandler(
		pool,
		createHandler,
		getHandler,
		finishHandler,
		responses,
		logger,
		cfg.DB.QueryTimeout,
	)

	router, err := httpapi.NewRouter(handler)
	if err != nil {
		return nil, err
	}

	app.Server = &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           router,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	return app, nil
}

func (c *Container) Close(ctx context.Context) error {
	// Финальный сбор gauge обращается к PostgreSQL
	err := c.shutdownTelemetry(ctx)

	if c.tripMetrics != nil {
		err = errors.Join(err, c.tripMetrics.Close())
	}

	if c.pool != nil {
		c.pool.Close()
	}

	return err
}
