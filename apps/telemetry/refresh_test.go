package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefreshRejectsInvalidDeviceResponse(t *testing.T) {
	for _, body := range []string{"not JSON", `[{"id":1,"type":"temperature","location":"Kitchen"}]`} {
		devices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/internal/v1/devices" {
				t.Errorf("unexpected URL: %s", r.URL)
			}
			fmt.Fprint(w, body)
		}))
		s := &server{devicesURL: devices.URL, client: devices.Client()}
		if _, err := s.refresh(context.Background(), []int{1}, ""); err == nil {
			t.Fatalf("accepted invalid device snapshot: %s", body)
		}
		devices.Close()
	}
}

func TestRefreshSkipsMissingAndNonTemperatureDevices(t *testing.T) {
	devices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":1,"type":"camera","location":"Kitchen","last_event_id":1}]`)
	}))
	defer devices.Close()
	s := &server{devicesURL: devices.URL, client: devices.Client()}
	ids, err := s.refresh(context.Background(), []int{1, 2}, "")
	if err != nil || len(ids) != 0 {
		t.Fatalf("unexpected devices for polling: %v %v", ids, err)
	}
}
