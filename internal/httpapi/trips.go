package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"github.com/google/uuid"
	"github.com/LuCh-Ans/template/internal/domain"
	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/trip"
)

const maxBodyBytes = 1 << 20

func (h *Handler) CreateTrip(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	req, err := decodeTripData(w, r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", err.Error())
		return
	}

	created, err := h.trips.CreateTrip(r.Context(), trip.CreateTripInput{
		UserID: req.UserId,
		DriverID: req.DriverId,
		Start: domain.Point{Latitude: req.StartPoint.Latitude, Longitude: req.StartPoint.Longitude},
		End: domain.Point{Latitude: req.EndPoint.Latitude, Longitude: req.EndPoint.Longitude},
		Price: req.Price,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Location", "/api/v1/trips/"+created.ID.String())
	writeJSON(w, http.StatusCreated, toAPITrip(created))
}

func (h *Handler) GetTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	t, err := h.trips.GetTrip(r.Context(), uuid.UUID(tripId))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPITrip(t))
}

func (h *Handler) FinishTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	t, err := h.trips.FinishTrip(r.Context(), uuid.UUID(tripId))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPITrip(t))
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrTripNotFound):
		writeProblem(w, r, http.StatusNotFound, "trip_not_found", "Trip not found", "Trip was not found")
	case errors.Is(err, domain.ErrTripCompleted):
		writeProblem(w, r, http.StatusConflict, "trip_completed", "Trip completed", "Operation is not allowed for a completed trip")
	case errors.Is(err, domain.ErrDriverBusy):
		writeProblem(w, r, http.StatusConflict, "driver_busy", "Driver busy", "Driver already has an active trip")
	default:
		h.logger.Error("internal error", "error", err, "method", r.Method, "path", r.URL.Path)
		writeProblem(w, r, http.StatusInternalServerError, "internal_error", "Internal Server Error", "Internal server error")
	}
}

func decodeTripData(w http.ResponseWriter, r *http.Request) (api.TripData, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return api.TripData{}, errors.New("cannot read request body")
	}

	if err := checkRequired(body); err != nil {
		return api.TripData{}, err
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var req api.TripData
	if err := dec.Decode(&req); err != nil {
		return api.TripData{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return api.TripData{}, errors.New("unexpected data after JSON object")
	}
	if err := validateTripData(req); err != nil {
		return api.TripData{}, err
	}
	return req, nil
}

func checkRequired(body []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil || top == nil {
		return errors.New("body must be a JSON object")
	}
	if err := requireKeys(top, "", "user_id", "driver_id", "start_point", "end_point", "price"); err != nil {
		return err
	}
	for _, name := range []string{"start_point", "end_point"} {
		var point map[string]json.RawMessage
		if err := json.Unmarshal(top[name], &point); err != nil || point == nil {
			return fmt.Errorf("%s must be an object", name)
		}
		if err := requireKeys(point, name+".", "latitude", "longitude"); err != nil {
			return err
		}
	}
	return nil
}

func requireKeys(obj map[string]json.RawMessage, prefix string, keys ...string) error {
	for _, k := range keys {
		if v, ok := obj[k]; !ok || string(v) == "null" {
			return fmt.Errorf("%s%s is required", prefix, k)
		}
	}
	return nil
}

func validateTripData(d api.TripData) error {
	if d.UserId == uuid.Nil {
		return errors.New("user_id must not be empty")
	}
	if d.DriverId == uuid.Nil {
		return errors.New("driver_id must not be empty")
	}
	if err := validatePoint("start_point", d.StartPoint); err != nil {
		return err
	}
	if err := validatePoint("end_point", d.EndPoint); err != nil {
		return err
	}
	if d.Price < 0 {
		return errors.New("price must be >= 0")
	}
	return nil
}

func validatePoint(name string, p api.Coordinates) error {
	if p.Latitude < -90 || p.Latitude > 90 {
		return fmt.Errorf("%s.latitude must be in [-90, 90]", name)
	}
	if p.Longitude < -180 || p.Longitude > 180 {
		return fmt.Errorf("%s.longitude must be in [-180, 180]", name)
	}
	return nil
}

func toAPITrip(t domain.Trip) api.Trip {
	return api.Trip{
		Id: t.ID,
		UserId: t.UserID,
		DriverId: t.DriverID,
		StartPoint: api.Coordinates{Latitude: t.Start.Latitude, Longitude: t.Start.Longitude},
		EndPoint: api.Coordinates{Latitude: t.End.Latitude, Longitude: t.End.Longitude},
		Price: t.Price,
		Status: api.TripStatus(t.Status),
		StartedAt: t.StartedAt,
		FinishedAt: t.FinishedAt,
	}
}
