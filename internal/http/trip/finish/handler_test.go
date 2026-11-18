package finish_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/http/response"
	action "github.com/LuCh-Ans/template/internal/http/trip/finish"
)

func TestHandler(t *testing.T) {
	internalErr := errors.New("private database connection failure")
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "success", status: http.StatusOK},
		{name: "missing", err: domain.ErrTripNotFound, status: http.StatusNotFound, code: "trip_not_found"},
		{name: "already completed", err: domain.ErrTripCompleted, status: http.StatusConflict, code: "trip_completed"},
		{name: "repository failure", err: internalErr, status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usecase := NewMocktripUseCase(gomock.NewController(t))
			id := uuid.New()
			at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			trip := domain.Trip{ID: id, UserID: uuid.New(), DriverID: uuid.New(), Status: domain.StatusCompleted, FinishedAt: &at}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/trips/"+id.String()+"/finish", nil)
			usecase.EXPECT().FinishTrip(request.Context(), id).Return(trip, tt.err)
			writer := response.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
			recorder := httptest.NewRecorder()
			action.New(usecase, writer).Handle(recorder, request, id)
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body.String())
			}
			if tt.err != nil {
				var problem api.Problem
				if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if problem.Code != tt.code || int(problem.Status) != tt.status || problem.Instance == nil || *problem.Instance != request.URL.Path {
					t.Errorf("unexpected problem: %#v", problem)
				}
				if recorder.Header().Get("Content-Type") != "application/problem+json" {
					t.Error("missing problem content type")
				}
				if strings.Contains(recorder.Body.String(), internalErr.Error()) {
					t.Error("internal details leaked")
				}
				return
			}
			var got api.Trip
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Id != id || got.UserId != trip.UserID || got.DriverId != trip.DriverID || got.Status != api.TripStatus(domain.StatusCompleted) || got.FinishedAt == nil || !got.FinishedAt.Equal(at) {
				t.Errorf("unexpected trip: %#v", got)
			}
			if recorder.Header().Get("Content-Type") != "application/json" {
				t.Error("missing JSON content type")
			}
		})
	}
}
