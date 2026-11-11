package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceServiceHonorsCancellation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("canceled request reached devices")
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewDeviceService(upstream.URL).GetSensors(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
