package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httpapi "github.com/LuCh-Ans/template/internal/http"
	"github.com/LuCh-Ans/template/internal/http/response"
)

func TestRouterFallbackProblems(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpapi.NewHandler(nil, nil, nil, nil, response.New(logger), logger, time.Second)
	router, err := httpapi.NewRouter(handler)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, code, allow string
		status                    int
	}{
		{http.MethodGet, "/unknown", "not_found", "", 404},
		{http.MethodDelete, "/api/v1/trips", "method_not_allowed", "POST", 405},
		{http.MethodPost, "/api/v1/trips/0b59469a-54c7-4256-a326-9aeb850632bb", "method_not_allowed", "GET", 405},
	} {
		t.Run(tc.code+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status || rec.Header().Get("Content-Type") != "application/problem+json" || rec.Header().Get("Allow") != tc.allow {
				t.Fatalf("response: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
			}
			var problem struct {
				Status                              int
				Code, Type, Title, Detail, Instance string
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Status != tc.status || problem.Code != tc.code || problem.Instance != tc.path || problem.Type == "" || problem.Title == "" || problem.Detail == "" {
				t.Fatalf("problem: %+v", problem)
			}
		})
	}
}
