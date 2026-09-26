package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"
	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/trip"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

const readyTimeout = time.Second

// Handler реализует сгенерированный api.ServerInterface
type Handler struct {
	pool   *pgxpool.Pool
	trips  *trip.Service
	logger *slog.Logger
}

// Если Handler перестанет соответствовать интерфейсу, проект не соберётся
var _ api.ServerInterface = (*Handler)(nil)

func NewHandler(pool *pgxpool.Pool, trips *trip.Service, logger *slog.Logger) *Handler {
	return &Handler{pool: pool, trips: trips, logger: logger}
}

// NewRouter собирает chi-роутер = middleware + сгенерированные маршруты
func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	return api.HandlerWithOptions(h, api.ChiServerOptions{
		BaseRouter: r,
		// Вызывается, когда сгенерированный код не смог разобрать параметры
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", err.Error())
		},
	})
}

// Health - liveness процесс жив в базу не ходит специально
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

// Ready - readiness готов ли сервис обслуживать запросы, то есть доступна ли база
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()

	if err := h.pool.Ping(ctx); err != nil {
		h.logger.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}
	writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}
