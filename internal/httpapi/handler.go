package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/trip"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Проверка готовности бд с таймаутом 1 секунда
const readyTimeout = time.Second

// Рализует сгенерированный api.ServerInterface, адаптер междк HTTP и сервисом
type Handler struct {
	pool *pgxpool.Pool
	trips *trip.Service
	logger *slog.Logger
	readyTimeout time.Duration
}

// Если Handler перестанет соответствовать интерфейсу, проект не соберётся
var _ api.ServerInterface = (*Handler)(nil)

func NewHandler(pool *pgxpool.Pool, trips *trip.Service, logger *slog.Logger, readyTimeout time.Duration) *Handler {
	return &Handler{pool: pool, trips: trips, logger: logger, readyTimeout: readyTimeout}
}

// NewRouter собирает chi-роутер = middleware + сгенерированные маршруты
func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(h.recoverer)

	return api.HandlerWithOptions(h, api.ChiServerOptions{
		BaseRouter: r,
		// Вызывается, когда сгенерированный код не смог разобрать параметры
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", err.Error())
		},
	})
}

// Ловит панику в хендлере, пишет её со стеком в лог и отвечает 500 в problem+json, как требует контракт, процесс продолжает работать
func (h *Handler) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// Специальная паника net/http оборвать соединение, отдаём серверу как есть
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			h.logger.Error("panic in handler",
				"panic", rec,
				"method", r.Method,
				"path", r.URL.Path,
				"stack", string(debug.Stack()),
			)
			writeProblem(w, r, http.StatusInternalServerError, "internal_error", "Internal Server Error", "Internal server error")
		}()

		next.ServeHTTP(w, r)
	})
}

// Жив ли процесс
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

// Готов ли сервис обслуживать запросы, то есть доступна ли база
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.readyTimeout)
	defer cancel()

	if err := h.pool.Ping(ctx); err != nil {
		h.logger.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}
	writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}