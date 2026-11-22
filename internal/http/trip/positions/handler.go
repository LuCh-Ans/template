package positions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/http/response"
)

const maxBodyBytes = 1 << 20

type positionSaver interface {
	SavePosition(ctx context.Context, position domain.Position) (domain.Position, error)
}

type positionLister interface {
	ListPositions(ctx context.Context, id uuid.UUID) ([]domain.Position, error)
}

type Handler struct {
	saver     positionSaver
	lister    positionLister
	responses *response.Writer
}

func New(saver positionSaver, lister positionLister, responses *response.Writer) *Handler {
	return &Handler{saver: saver, lister: lister, responses: responses}
}

type positionRequest struct {
	Coordinates *struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	} `json:"coordinates"`
	RecordedAt *time.Time `json:"recorded_at"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request, id api.TripId) {
	req, err := decodePosition(w, r)
	if err != nil {
		h.responses.Problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", err.Error())
		return
	}

	saved, err := h.saver.SavePosition(r.Context(), domain.Position{
		TripID: id,
		Coordinates: domain.Point{
			Latitude:  *req.Coordinates.Latitude,
			Longitude: *req.Coordinates.Longitude,
		},
		RecordedAt: *req.RecordedAt,
	})
	if err != nil {
		h.responses.Error(w, r, err)
		return
	}

	h.responses.JSON(w, r, http.StatusCreated, toAPI(saved))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request, id api.TripId) {
	points, err := h.lister.ListPositions(r.Context(), id)
	if err != nil {
		h.responses.Error(w, r, err)
		return
	}

	items := make([]api.TripPosition, 0, len(points))
	for _, point := range points {
		items = append(items, toAPI(point))
	}
	h.responses.JSON(w, r, http.StatusOK, api.TripPositionsResponse{Positions: items})
}

func decodePosition(w http.ResponseWriter, r *http.Request) (positionRequest, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	var req positionRequest
	if err := decoder.Decode(&req); err != nil {
		return positionRequest{}, errors.New("body must contain valid coordinates and recorded_at")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return positionRequest{}, errors.New("unexpected data after JSON object")
	}
	if req.Coordinates == nil || req.Coordinates.Latitude == nil ||
		req.Coordinates.Longitude == nil || req.RecordedAt == nil {
		return positionRequest{}, errors.New("coordinates.latitude, coordinates.longitude and recorded_at are required")
	}
	return req, nil
}

func toAPI(p domain.Position) api.TripPosition {
	return api.TripPosition{
		Id:     p.ID,
		TripId: p.TripID,
		Coordinates: api.Coordinates{
			Latitude:  p.Coordinates.Latitude,
			Longitude: p.Coordinates.Longitude,
		},
		RecordedAt: p.RecordedAt,
	}
}
