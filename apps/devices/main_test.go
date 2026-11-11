package main

import (
	"devices/handlers"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterRejectsInvalidDevice(t *testing.T) {
	router := gin.New()
	handlers.NewDeviceHandler(nil).RegisterRoutes(router.Group("/api/v1"))
	for _, body := range []string{
		"{", "null", `{}`, `{"type_id":1,"name":" ","protocol":"http"}`,
		`{"type_id":0,"name":"Device","protocol":"http"}`,
		`{"type_id":1,"name":"Device","protocol":"http","location_id":0}`,
		`{"type_id":1,"name":"Device","protocol":"http","location_id":null}`,
		`{"type_id":1,"name":"Device","protocol":"http","owner_id":2}`,
		`{"name":"Old sensor","type":"temperature","location":"Kitchen"}`,
		`{"type_id":1,"name":"Device","protocol":"http"} {}`,
		strings.Repeat("x", 4097),
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/devices", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatalf("%q: got %d, want 400", body, response.Code)
		}
		if !strings.Contains(response.Body.String(), `"code"`) || !strings.Contains(response.Body.String(), `"message"`) {
			t.Fatalf("invalid error response: %s", response.Body)
		}
	}
}
func TestDeviceRoutesUseDeviceIDs(t *testing.T) {
	router := gin.New()
	h := handlers.NewDeviceHandler(nil)
	h.RegisterRoutes(router.Group("/api/v1"))
	h.RegisterInternalRoutes(router.Group("/internal/v1"))
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"DELETE", "/api/v1/devices/0", 400},
		{"DELETE", "/api/v1/devices/bad", 400},
		{"GET", "/internal/v1/devices/bad", 400},
		{"GET", "/api/v1/sensors", 404},
		{"POST", "/api/v1/sensors", 404},
		{"GET", "/api/v1/devices/1/camera-config", 404},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, response.Code)
		}
	}
}
