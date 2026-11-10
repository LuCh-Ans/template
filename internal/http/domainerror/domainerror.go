package domainerror

import (
	"errors"
	"net/http"
	"strings"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	api "github.com/LuCh-Ans/template/internal/generated"
)

func Problem(err error) (int, api.Problem) {
	status := http.StatusInternalServerError
	code := "internal_error"
	title := "Internal Server Error"
	detail := "Internal server error"

	var validationErr *domain.ValidationError

	switch {
	case errors.As(err, &validationErr):
		status = http.StatusBadRequest
		code = "invalid_request"
		title = "Invalid request"
		detail = validationErr.Error()

	case errors.Is(err, domain.ErrTripNotFound):
		status = http.StatusNotFound
		code = "trip_not_found"
		title = "Trip not found"
		detail = "Trip was not found"

	case errors.Is(err, domain.ErrTripCompleted):
		status = http.StatusConflict
		code = "trip_completed"
		title = "Trip completed"
		detail = "Operation is not allowed for a completed trip"

	case errors.Is(err, domain.ErrDriverBusy):
		status = http.StatusConflict
		code = "driver_busy"
		title = "Driver busy"
		detail = "Driver already has an active trip"

	case errors.Is(err, domain.ErrIdempotencyConflict):
		status = http.StatusConflict
		code = "idempotency_conflict"
		title = "Idempotency conflict"
		detail = "Idempotency-Key was already used with a different request body"
	}

	return status, api.Problem{
		Type:   "https://tripgo.example/problems/" + strings.ReplaceAll(code, "_", "-"),
		Title:  title,
		Status: int32(status),
		Code:   code,
		Detail: &detail,
	}
}
