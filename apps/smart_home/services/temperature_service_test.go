package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetTemperatureThroughTelemetry(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry" || r.URL.Query().Get("location") != "Living Room & кухня" || len(r.URL.Query()) != 1 {
			t.Errorf("unexpected URL: %s", r.URL)
		}
		fmt.Fprint(w, `{"value":23.5,"unit":"°C","status":"active"}`)
	}))
	defer upstream.Close()
	data, err := NewTemperatureService(upstream.URL).GetTemperature(context.Background(), "Living Room & кухня")
	if err != nil || data.Value != 23.5 || data.Status != "active" {
		t.Fatalf("unexpected response: %+v, error: %v", data, err)
	}
}
