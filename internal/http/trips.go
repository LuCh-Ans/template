package httpapi

import (
	"net/http"

	api "github.com/LuCh-Ans/template/internal/generated"
)

func (h *Handler) CreateTrip(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	h.createHandler.Handle(w, r, params)
}

func (h *Handler) GetTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	h.getHandler.Handle(w, r, tripId)
}

func (h *Handler) FinishTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	h.finishHandler.Handle(w, r, tripId)
}

func (h *Handler) CreateTripPosition(w http.ResponseWriter, r *http.Request, id api.TripId) {
	h.positionsHandler.Create(w, r, id)
}

func (h *Handler) ListTripPositions(w http.ResponseWriter, r *http.Request, id api.TripId) {
	h.positionsHandler.List(w, r, id)
}
