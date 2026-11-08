package finish

import (
	"net/http"

	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/http/response"
	"github.com/LuCh-Ans/template/internal/http/trip/convert"
)

type Handler struct {
	usecase   tripUseCase
	responses *response.Writer
}

func New(usecase tripUseCase, responses *response.Writer) *Handler {
	return &Handler{
		usecase:   usecase,
		responses: responses,
	}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, id api.TripId) {
	trip, err := h.usecase.FinishTrip(r.Context(), id)
	if err != nil {
		h.responses.Error(w, r, err)
		return
	}
	h.responses.JSON(w, r, http.StatusOK, convert.ToAPI(trip))
}
