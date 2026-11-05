package create

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	domain "github.com/LuCh-Ans/template/internal/domain/trip"
	api "github.com/LuCh-Ans/template/internal/generated"
	"github.com/LuCh-Ans/template/internal/http/response"
	"github.com/LuCh-Ans/template/internal/http/trip/convert"
	createusecase "github.com/LuCh-Ans/template/internal/usecase/trip/create"
)

const maxBodyBytes = 1 << 20

type Handler struct {
	creator   tripCreator
	responses *response.Writer
}

func New(creator tripCreator, responses *response.Writer) *Handler {
	return &Handler{
		creator:   creator,
		responses: responses,
	}
}

// Хендлеры
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	req, err := decodeTripData(w, r)
	if err != nil {
		h.responses.Problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", err.Error())
		return
	}

	t, created, err := h.creator.CreateTrip(r.Context(), createusecase.CreateTripInput{
		UserID:         req.UserId,
		DriverID:       req.DriverId,
		Start:          domain.Point{Latitude: req.StartPoint.Latitude, Longitude: req.StartPoint.Longitude},
		End:            domain.Point{Latitude: req.EndPoint.Latitude, Longitude: req.EndPoint.Longitude},
		Price:          req.Price,
		IdempotencyKey: params.IdempotencyKey,
	})
	if err != nil {
		h.responses.Error(w, r, err)
		return
	}

	// Повтор с тем же Idempotency-Key: та же поездка, но 200, а не 201
	if !created {
		h.responses.JSON(w, http.StatusOK, convert.ToAPI(t))
		return
	}
	w.Header().Set("Location", "/api/v1/trips/"+t.ID.String())
	h.responses.JSON(w, http.StatusCreated, convert.ToAPI(t))
}

// Чтение и проверка тела
func decodeTripData(w http.ResponseWriter, r *http.Request) (api.TripData, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return api.TripData{}, errors.New("cannot read request body")
	}
	// Есть ли обязательные поля
	if err := checkRequired(body); err != nil {
		return api.TripData{}, err
	}
	// Строгий разбор
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields() // запрет на лишние поля, чтобы не было опечаток в JSON
	var req api.TripData
	if err := dec.Decode(&req); err != nil {
		return api.TripData{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return api.TripData{}, errors.New("unexpected data after JSON object")
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
