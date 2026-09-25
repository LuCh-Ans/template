package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	api "github.com/LuCh-Ans/template/internal/generated"
)

const readyTimeout = time.Second

// Handler реализует сгенерированный api.ServerInterface
type Handler struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// Проверка на этапе компиляции: если Handler перестанет соответствовать
// интерфейсу (например, контракт поменялся), проект не соберётся
var _ api.ServerInterface = (*Handler)(nil)

func NewHandler(pool *pgxpool.Pool, logger *slog.Logger) *Handler {
	return &Handler{pool: pool, logger: logger}
}

// NewRouter собирает chi-роутер: middleware + сгенерированные маршруты
func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	return api.HandlerWithOptions(h, api.ChiServerOptions{
		BaseRouter: r,
		// Вызывается, когда сгенерированный код не смог разобрать параметры,
		// например tripId не UUID. По умолчанию там plain text, нам нужен problem+json.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", err.Error())
		},
	})
}

// Health - liveness: процесс жив в базу не ходит специально
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

// Ready -readiness: готов ли сервис обслуживать запросы, то есть доступна ли база
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

func (h *Handler) CreateTrip(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	writeProblem(w, r, http.StatusNotImplemented, "internal_error", "Not implemented", "not implemented yet")
}

func (h *Handler) GetTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	writeProblem(w, r, http.StatusNotImplemented, "internal_error", "Not implemented", "not implemented yet")
}

func (h *Handler) FinishTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	writeProblem(w, r, http.StatusNotImplemented, "internal_error", "Not implemented", "not implemented yet")
}
