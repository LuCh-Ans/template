package telemetry_test

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/mock/gomock"

	"github.com/LuCh-Ans/template/internal/infra/telemetry"
)

func TestTripMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	defer otel.SetMeterProvider(previous)
	defer func() {
		if err := provider.Shutdown(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	source := NewMockactiveTripCounter(gomock.NewController(t))
	metrics, err := telemetry.NewTripMetrics(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := metrics.Close(); err != nil {
			t.Error(err)
		}
	}()
	metrics.TripCreated(t.Context())
	metrics.TripCreated(t.Context())
	metrics.TripCompleted(t.Context())

	for _, count := range []int64{7, 3, 0} {
		source.EXPECT().CountActive(gomock.Any()).Return(count, nil)
		var data metricdata.ResourceMetrics
		if err := reader.Collect(t.Context(), &data); err != nil {
			t.Fatal(err)
		}
		values := map[string]int64{}
		for _, scope := range data.ScopeMetrics {
			for _, instrument := range scope.Metrics {
				switch value := instrument.Data.(type) {
				case metricdata.Sum[int64]:
					if !value.IsMonotonic || len(value.DataPoints) != 1 {
						t.Fatalf("invalid counter: %+v", value)
					}
					if value.DataPoints[0].Attributes.Len() != 0 {
						t.Fatal("business counter has labels")
					}
					values[instrument.Name] = value.DataPoints[0].Value
				case metricdata.Gauge[int64]:
					if len(value.DataPoints) != 1 {
						t.Fatalf("invalid gauge: %+v", value)
					}
					values[instrument.Name] = value.DataPoints[0].Value
				}
			}
		}
		for name, want := range map[string]int64{"tripgo.trips.created": 2, "tripgo.trips.completed": 1, "tripgo.trips.active": count} {
			if got, ok := values[name]; !ok || got != want {
				t.Fatalf("%s = %d (present=%v), want %d", name, got, ok, want)
			}
		}
	}
	sourceErr := errors.New("database unavailable")
	source.EXPECT().CountActive(gomock.Any()).Return(int64(0), sourceErr)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); !errors.Is(err, sourceErr) {
		t.Fatalf("collection error = %v", err)
	}
	for _, scope := range data.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if value, ok := instrument.Data.(metricdata.Gauge[int64]); ok && len(value.DataPoints) > 0 {
				t.Fatalf("gauge reported value on error: %+v", value)
			}
		}
	}
	if err := metrics.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
}
