// Сгенерировано нейросетью.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTemperature(t *testing.T) {
	handler := routes()
	for _, tc := range []struct {
		path     string
		location string
		id       string
	}{
		{"/temperature?location=Living%20Room", "Living Room", "1"},
		{"/temperature?location=Bedroom", "Bedroom", "2"},
		{"/temperature?location=Kitchen", "Kitchen", "3"},
		{"/temperature?location=Office", "Office", "0"},
		{"/temperature/1", "Living Room", "1"},
		{"/temperature/2", "Bedroom", "2"},
		{"/temperature/3", "Kitchen", "3"},
		{"/temperature/99", "Unknown", "99"},
		{"/temperature", "Unknown", "0"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			values := make(map[float64]bool)
			for range 5 {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
				if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("unexpected response: %v", response)
				}
				var data temperatureResponse
				if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
					t.Fatal(err)
				}
				if data.Location != tc.location || data.SensorID != tc.id || data.Unit != "°C" || data.Status != "active" || data.SensorType != "temperature" || data.Timestamp.IsZero() || data.Value < 15 || data.Value >= 30 {
					t.Fatalf("unexpected temperature: %+v", data)
				}
				values[data.Value] = true
			}
			if len(values) == 1 {
				t.Fatal("temperature did not change across requests")
			}
		})
	}
}

func TestRoutesRejectInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodPost, "/temperature", http.StatusMethodNotAllowed},
		{http.MethodGet, "/missing", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		routes().ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s %s: got %d, want %d", tc.method, tc.path, response.Code, tc.status)
		}
	}
}
