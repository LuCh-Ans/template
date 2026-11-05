package container

import (
	"context"
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
	triprepo "github.com/LuCh-Ans/template/internal/repository/trip"
	"github.com/LuCh-Ans/template/internal/usecase/trip/create"
	"github.com/LuCh-Ans/template/internal/usecase/trip/finish"
	"github.com/LuCh-Ans/template/internal/usecase/trip/get"
)

type Container struct {
	Server *http.Server
	Logger *slog.Logger
	pool   *pgxpool.Pool
}

func New(ctx context.Context, cfg config.Config) (*Container, error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	pool, err := postgres.NewPool(ctx, cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("initialize postgres: %w", err)
	}
	logger.Info("connected to database")

	txManager := transaction.NewTxManager(pool, cfg.DB.QueryTimeout)
	tripRepo := triprepo.NewTripRepository(txManager, cfg.DB.QueryTimeout)

	createTrip := create.New(txManager, tripRepo, cfg.IdempotencyTTL, time.Now)
	getTrip := get.New(tripRepo)
	finishTrip := finish.New(txManager, tripRepo, time.Now)

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
	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           httpapi.NewRouter(handler),
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	return &Container{
		Server: server,
		Logger: logger,
		pool:   pool,
	}, nil
}

func (c *Container) Close() {
	c.pool.Close()
}
