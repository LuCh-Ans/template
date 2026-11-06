package create_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/http/response"
	createhttp "github.com/LuCh-Ans/template/internal/http/trip/create"
	createusecase "github.com/LuCh-Ans/template/internal/usecase/trip/create"
)

func requestData() api.TripData {
	return api.TripData{
		UserId: uuid.New(), DriverId: uuid.New(), Price: 1450,
		StartPoint: api.Coordinates{Latitude: 59.9, Longitude: 30.3},
		EndPoint:   api.Coordinates{Latitude: 60, Longitude: 31},
	}
}

func TestCreateHandler(t *testing.T) {
	internalErr := errors.New("private database connection failure")
	tests := []struct {
		name    string
		created bool
		err     error
		status  int
		code    string
	}{
		{name: "created", created: true, status: http.StatusCreated},
		{name: "replay", status: http.StatusOK},
		{name: "driver busy", err: domain.ErrDriverBusy, status: http.StatusConflict, code: "driver_busy"},
		{name: "key conflict", err: domain.ErrIdempotencyConflict, status: http.StatusConflict, code: "idempotency_conflict"},
		{name: "domain validation", err: &domain.ValidationError{Field: "price", Message: "must be >= 0"}, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "repository failure", err: internalErr, status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usecase := NewMocktripCreator(gomock.NewController(t))
			data := requestData()
			body, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/trips", bytes.NewReader(body))
			key := uuid.New()
			input := createusecase.CreateTripInput{
				UserID: data.UserId, DriverID: data.DriverId, Price: data.Price, IdempotencyKey: &key,
				Start: domain.Point{Latitude: data.StartPoint.Latitude, Longitude: data.StartPoint.Longitude},
				End:   domain.Point{Latitude: data.EndPoint.Latitude, Longitude: data.EndPoint.Longitude},
			}
			trip := domain.Trip{
				ID: uuid.New(), UserID: input.UserID, DriverID: input.DriverID,
				Start: input.Start, End: input.End, Price: input.Price, Status: domain.StatusActive,
				StartedAt: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
			}
			usecase.EXPECT().CreateTrip(request.Context(), input).Return(trip, tt.created, tt.err)
			writer := response.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
			recorder := httptest.NewRecorder()
			createhttp.New(usecase, writer).Handle(recorder, request, api.CreateTripParams{IdempotencyKey: &key})
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
					t.Error("internal error leaked into the response")
				}
				return
			}
			var got api.Trip
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			want := api.Trip{
				Id: trip.ID, UserId: data.UserId, DriverId: data.DriverId, StartPoint: data.StartPoint,
				EndPoint: data.EndPoint, Price: data.Price, Status: api.TripStatus(domain.StatusActive), StartedAt: trip.StartedAt,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("response = %#v, want %#v", got, want)
			}
			location := ""
			if tt.created {
				location = "/api/v1/trips/" + trip.ID.String()
			}
			if recorder.Header().Get("Location") != location || recorder.Header().Get("Content-Type") != "application/json" {
				t.Errorf("unexpected headers: %v", recorder.Header())
			}
		})
	}
}

func TestCreateHandlerInvalidBody(t *testing.T) {
	data := requestData()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var valid map[string]any
	if err := json.Unmarshal(encoded, &valid); err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		change func(map[string]any)
	}{
		{name: "missing price", change: func(body map[string]any) { delete(body, "price") }},
		{name: "null price", change: func(body map[string]any) { body["price"] = nil }},
		{name: "null point", change: func(body map[string]any) { body["start_point"] = nil }},
		{name: "missing coordinate", change: func(body map[string]any) { body["end_point"] = map[string]any{"latitude": 0} }},
		{name: "unknown field", change: func(body map[string]any) { body["unknown"] = 1 }},
		{name: "invalid UUID", change: func(body map[string]any) { body["user_id"] = "bad-id" }},
		{name: "wrong price type", change: func(body map[string]any) { body["price"] = "free" }},
	}
	type invalidBodyCase struct {
		name string
		body string
	}
	tests := make([]invalidBodyCase, 0, 6+len(mutations))
	tests = append(tests,
		invalidBodyCase{name: "malformed", body: "{"},
		invalidBodyCase{name: "null", body: "null"},
		invalidBodyCase{name: "array", body: "[]"},
		invalidBodyCase{name: "empty object", body: "{}"},
		invalidBodyCase{name: "trailing object", body: string(encoded) + "{}"},
		invalidBodyCase{name: "too large", body: strings.Repeat(" ", (1<<20)+1)},
	)
	for _, mutation := range mutations {
		body := make(map[string]any, len(valid))
		for key, value := range valid {
			body[key] = value
		}
		mutation.change(body)
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		tests = append(tests, invalidBodyCase{name: mutation.name, body: string(encoded)})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usecase := NewMocktripCreator(gomock.NewController(t))
			writer := response.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
			request := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(tt.body))
			recorder := httptest.NewRecorder()
			createhttp.New(usecase, writer).Handle(recorder, request, api.CreateTripParams{})
			var problem api.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusBadRequest || problem.Code != "invalid_request" {
				t.Fatalf("status/body = %d/%#v", recorder.Code, problem)
			}
		})
	}
}
