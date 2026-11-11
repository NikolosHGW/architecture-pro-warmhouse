package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"smarthome/services"

	"github.com/gin-gonic/gin"
)

func TestSensorHandlersUseServiceClients(t *testing.T) {
	const sensor = `{"id":1,"owner_id":1,"type_id":1,"location_id":2,"name":"Sensor","protocol":"http","type":"temperature","location":"Living Room","value":0,"unit":"°C","status":"inactive","created_at":"2026-11-11T12:00:00Z","last_updated":"2026-11-11T12:00:00Z"}`
	devices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/internal/v1/device-types" {
			if r.URL.Query().Get("code") != "temperature" {
				t.Errorf("wrong type lookup: %s", r.URL)
			}
			fmt.Fprint(w, `{"id":1,"code":"temperature"}`)
			return
		}
		if r.URL.Path == "/internal/v1/locations" {
			var location struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&location); err != nil || location.Name != "Living Room" {
				t.Errorf("wrong location request: %+v %v", location, err)
			}
			fmt.Fprint(w, `{"id":2,"name":"Living Room"}`)
			return
		}
		if r.URL.Path == "/internal/v1/devices/99" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"code":"device_not_found","message":"Device not found"}`)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path == "/internal/v1/devices" {
				fmt.Fprint(w, "["+sensor+","+strings.Replace(sensor, `"id":1`, `"id":2`, 1)+"]")
			} else {
				fmt.Fprint(w, sensor)
			}
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("invalid service request: body=%v, error=%v", body, err)
			}
			if r.Method == http.MethodPatch {
				if r.URL.Path != "/internal/v1/devices/1/value" || body["value"] != 22.5 || body["status"] != "active" {
					t.Errorf("invalid value update: %s %v", r.URL, body)
				}
			} else if body["name"] != "Sensor" {
				t.Errorf("missing device name: %v", body)
			}
			if r.Method == http.MethodPost {
				if r.URL.Path != "/api/v1/devices" || body["type_id"] != float64(1) || body["location_id"] != float64(2) || body["protocol"] != "http" {
					t.Errorf("wrong device registration: %s %v", r.URL, body)
				}
				if _, ok := body["type"]; ok {
					t.Error("legacy type leaked into device registration")
				}
				w.WriteHeader(http.StatusCreated)
			}
			fmt.Fprint(w, sensor)
		case http.MethodDelete:
			if r.URL.Path != "/api/v1/devices/1" {
				t.Errorf("invalid delete path: %s", r.URL)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
	}))
	defer devices.Close()
	var measurements atomic.Int32
	telemetry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/telemetry" {
			t.Errorf("invalid telemetry URL: %s", r.URL)
		}
		measurements.Add(1)
		const first = `{"id":7,"device_id":1,"value":23.5,"unit":"°C","location":"Living Room","status":"active","timestamp":"2026-11-11T12:00:00Z"}`
		const second = `{"id":8,"device_id":2,"value":26,"unit":"°C","location":"Living Room","status":"active","timestamp":"2026-11-11T12:00:00Z"}`
		switch r.URL.Query().Get("device_ids") {
		case "1,2":
			fmt.Fprint(w, "["+first+","+second+"]")
		case "1":
			fmt.Fprint(w, "["+first+"]")
		case "":
			if r.URL.Query().Get("location") != "Living Room" {
				t.Errorf("invalid location URL: %s", r.URL)
			}
			fmt.Fprint(w, first)
		default:
			t.Errorf("invalid device IDs: %s", r.URL)
		}
	}))
	defer telemetry.Close()
	h := NewSensorHandler(services.NewDeviceService(devices.URL), services.NewTemperatureService(telemetry.URL))
	router := gin.New()
	h.RegisterRoutes(router.Group("/api/v1"))
	router.GET("/api/v1/telemetry", h.GetTelemetry)
	for _, tc := range []struct {
		method, path, body, want string
		status                   int
	}{
		{"GET", "/api/v1/sensors", "", `"value":26`, 200},
		{"GET", "/api/v1/sensors/1", "", `"status":"active"`, 200},
		{"POST", "/api/v1/sensors", `{"name":"Sensor","type":"temperature","location":"Living Room","unit":"°C"}`, `"id":1`, 201},
		{"PUT", "/api/v1/sensors/1", `{"name":"Sensor"}`, `"name":"Sensor"`, 200},
		{"PATCH", "/api/v1/sensors/1/value", `{"value":22.5,"status":"active"}`, "updated successfully", 200},
		{"DELETE", "/api/v1/sensors/1", "", "deleted successfully", 200},
		{"GET", "/api/v1/sensors/temperature/Living%20Room", "", `"value":23.5`, 200},
		{"GET", "/api/v1/telemetry?location=Living%20Room", "", `"id":7`, 200},
		{"POST", "/api/v1/devices", `{"name":"Sensor","location":"Living Room"}`, "404", 404},
		{"GET", "/api/v1/devices", "", "404", 404},
		{"POST", "/api/v1/sensors", `{"name":"Sensor","location":"Living Room"}`, "error", 400},
		{"GET", "/api/v1/sensors/99", "", "Sensor not found", 404},
		{"GET", "/api/v1/sensors/bad", "", "Invalid sensor ID", 400},
		{"POST", "/api/v1/sensors", `{}`, "error", 400},
		{"GET", "/api/v1/telemetry", "", "location is required", 400},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.want) {
			t.Fatalf("%s %s: got %d %s", tc.method, tc.path, response.Code, response.Body)
		}
	}
	if got := measurements.Load(); got != 4 {
		t.Fatalf("got %d telemetry reads, want 4 (one batch for the sensor list)", got)
	}
}

func TestSensorHandlersReportDeviceFailures(t *testing.T) {
	devices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"code":"database_unavailable","message":"devices database unavailable"}`)
	}))
	defer devices.Close()
	h := NewSensorHandler(services.NewDeviceService(devices.URL), nil)
	response := httptest.NewRecorder()
	h.GetSensors(ginContext(response))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "devices database unavailable") {
		t.Fatalf("unexpected service error: %d %s", response.Code, response.Body)
	}
	devices.Close()
	response = httptest.NewRecorder()
	h.GetSensors(ginContext(response))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("unreachable devices: got %d, want 502", response.Code)
	}
}

func ginContext(response *httptest.ResponseRecorder) *gin.Context {
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/sensors", nil)
	return c
}
