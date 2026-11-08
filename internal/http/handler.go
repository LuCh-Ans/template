package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/http/middleware"
	"github.com/LuCh-Ans/template/internal/http/response"
	createhttp "github.com/LuCh-Ans/template/internal/http/trip/create"
	finishhttp "github.com/LuCh-Ans/template/internal/http/trip/finish"
	gethttp "github.com/LuCh-Ans/template/internal/http/trip/get"
)

// Рализует сгенерированный api.ServerInterface, адаптер между HTTP и сервисом
type Handler struct {
	readiness     readinessChecker // Ping базы
	createHandler *createhttp.Handler
	logger        *slog.Logger
	readyTimeout  time.Duration // сколько ждать Ping
	getHandler    *gethttp.Handler
	finishHandler *finishhttp.Handler
	responses     *response.Writer
}

// Если Handler перестанет соответствовать интерфейсу, проект не соберётся
var _ api.ServerInterface = (*Handler)(nil)

func NewHandler(readiness readinessChecker, createHandler *createhttp.Handler, getHandler *gethttp.Handler,
	finishHandler *finishhttp.Handler, responses *response.Writer,
	logger *slog.Logger, readyTimeout time.Duration,
) *Handler {
	return &Handler{
		readiness:     readiness,
		createHandler: createHandler,
		getHandler:    getHandler,
		finishHandler: finishHandler,
		responses:     responses,
		logger:        logger,
		readyTimeout:  readyTimeout,
	}
}

// NewRouter собирает chi-роутер = middleware + сгенерированные маршруты
func NewRouter(h *Handler) (http.Handler, error) {
	r := chi.NewRouter()

	api.HandlerWithOptions(h, api.ChiServerOptions{
		BaseRouter: r, // регистрация маршрутов в роутере r
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) { // ошибки problem+json
			detail := "Request parameters are invalid"
			var formatErr *api.InvalidParamFormatError
			if errors.As(err, &formatErr) {
				detail = fmt.Sprintf("parameter %s has invalid format", formatErr.ParamName)
			}
			h.responses.Problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", detail)
		},
	})
	return middleware.Wrap(r, h.logger, h.responses)
}

// Жив ли процесс
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	h.responses.JSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

// Готов ли сервис обслуживать запросы, то есть доступна ли база
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.readyTimeout)
	defer cancel()

	if err := h.readiness.Ping(ctx); err != nil {
		h.logger.WarnContext(r.Context(), "readiness check failed", "error", err)
		h.responses.JSON(w, r, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}
	h.responses.JSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}
