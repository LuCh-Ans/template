package domainerror_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	"github.com/LuCh-Ans/template/internal/http/domainerror"
)

func TestProblem(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
		title  string
		detail string
	}{
		{"not found", domain.ErrTripNotFound, http.StatusNotFound, "trip_not_found", "Trip not found", "Trip was not found"},
		{"completed", domain.ErrTripCompleted, http.StatusConflict, "trip_completed", "Trip completed", "Operation is not allowed for a completed trip"},
		{"busy", domain.ErrDriverBusy, http.StatusConflict, "driver_busy", "Driver busy", "Driver already has an active trip"},
		{"idempotency conflict", domain.ErrIdempotencyConflict, http.StatusConflict, "idempotency_conflict", "Idempotency conflict", "Idempotency-Key was already used with a different request body"},
		{"validation", &domain.ValidationError{Field: "price", Message: "must be >= 0"}, http.StatusBadRequest, "invalid_request", "Invalid request", "price must be >= 0"},
		{"internal", errors.New("database password=secret"), http.StatusInternalServerError, "internal_error", "Internal Server Error", "Internal server error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, err := range []error{tt.err, fmt.Errorf("operation failed: %w", tt.err)} {
				status, body := domainerror.Problem(err)
				if status != tt.status || int(body.Status) != tt.status {
					t.Fatalf("status = %d / %d, want %d", status, body.Status, tt.status)
				}
				if body.Code != tt.code || body.Title != tt.title {
					t.Errorf("problem = %#v, want code %q and title %q", body, tt.code, tt.title)
				}
				if body.Detail == nil || *body.Detail != tt.detail {
					t.Errorf("detail = %v, want %q", body.Detail, tt.detail)
				}
				wantType := "https://tripgo.example/problems/" + strings.ReplaceAll(tt.code, "_", "-")
				if body.Type != wantType || body.Instance != nil {
					t.Errorf("type/instance = %q/%v, want %q/nil", body.Type, body.Instance, wantType)
				}
			}
		})
	}
}
