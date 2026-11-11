package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLatestRequiresLocation(t *testing.T) {
	response := httptest.NewRecorder()
	(&server{}).latest(response, httptest.NewRequest(http.MethodGet, "/telemetry", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", response.Code)
	}
}

func TestPollRejectsBadTemperature(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusServiceUnavailable, "unavailable"},
		{http.StatusOK, "not JSON"},
		{http.StatusOK, `{}`},
		{http.StatusOK, `{"location":"Living Room & кухня","unit":"°C","status":"active","timestamp":"2026-11-11T12:00:00Z"}`},
		{http.StatusOK, `{"value":null,"location":"Living Room & кухня","unit":"°C","status":"active","timestamp":"2026-11-11T12:00:00Z"}`},
		{http.StatusOK, `{"location":"Bedroom","value":20,"unit":"°C","timestamp":"2026-11-11T12:00:00Z"}`},
	} {
		t.Run(tc.body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/temperature" || r.URL.Query().Get("location") != "Living Room & кухня" || len(r.URL.Query()) != 1 {
					t.Errorf("unexpected request URL: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer upstream.Close()
			s := &server{temperatureURL: upstream.URL, client: upstream.Client()}
			if err := s.pollDevice(context.Background(), device{ID: 1, Location: "Living Room & кухня", Version: 1}); err == nil {
				t.Fatal("invalid temperature response accepted")
			}
		})
	}
}

func TestParseDeviceIDs(t *testing.T) {
	ids, err := parseDeviceIDs("1,2,123")
	if err != nil || !reflect.DeepEqual(ids, []int{1, 2, 123}) {
		t.Fatalf("%v %v", ids, err)
	}
	for _, raw := range []string{"", "0", "-1", "1,,2", "one", "9223372036854775808"} {
		if _, err := parseDeviceIDs(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestDecodeDeviceEvent(t *testing.T) {
	good := `{"id":1,"device_id":2,"type":"device.created","payload":{"id":2,"type":"temperature","location":"Kitchen"}}`
	event, err := decodeEvent([]byte(good))
	if err != nil || event.ID != 1 || event.Payload.ID != 2 {
		t.Fatalf("%+v %v", event, err)
	}
	for _, raw := range []string{"{", "null", `{}`, strings.Replace(good, `"device_id":2`, `"device_id":3`, 1), strings.ReplaceAll(good, ":2", ":9223372036854775808"), strings.Replace(good, "device.created", "unknown", 1), strings.Repeat("x", 4097)} {
		if _, err := decodeEvent([]byte(raw)); err == nil {
			t.Errorf("accepted invalid event: %s", raw)
		}
	}
}
