package response

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/http/domainerror"
)

type Writer struct {
	logger *slog.Logger
}

func New(logger *slog.Logger) *Writer {
	return &Writer{logger: logger}
}

// Успешный json ответ
func (h *Writer) JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.Error("write json response", "error", err)
	}
}

// Ответ об ошибке
func (h *Writer) Problem(w http.ResponseWriter, r *http.Request, status int32, code, title, detail string) {
	problem := api.Problem{
		Type:   "https://tripgo.example/problems/" + strings.ReplaceAll(code, "_", "-"),
		Title:  title,
		Status: status,
		Code:   code,
		Detail: &detail,
	}

	h.writeProblemResponse(w, r, problem)
}

func (h *Writer) writeProblemResponse(w http.ResponseWriter, r *http.Request, problem api.Problem) {
	instance := r.URL.Path
	problem.Instance = &instance

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(int(problem.Status))

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		h.logger.ErrorContext(r.Context(), "write problem response", "error", err)
	}
}

// Превращает доменную ошибку в HTTP ответ
func (h *Writer) Error(w http.ResponseWriter, r *http.Request, err error) {
	status, problem := domainerror.Problem(err)

	if status >= http.StatusInternalServerError {
		h.logger.ErrorContext(
			r.Context(),
			"internal error",
			"error", err,
			"method", r.Method,
			"path", r.URL.Path,
		)
	}

	h.writeProblemResponse(w, r, problem)
}
