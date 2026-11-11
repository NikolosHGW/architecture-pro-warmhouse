package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegrationConsumerAndSynchronousReadings(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration test")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := pgx.Identifier{fmt.Sprintf("test_telemetry_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	initSQL, err := os.ReadFile("init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(initSQL)); err != nil {
		t.Fatal(err)
	}
	var polls atomic.Int32
	var active atomic.Bool
	active.Store(true)
	var healthy atomic.Bool
	healthy.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/devices" {
			if active.Load() {
				fmt.Fprint(w, `[{"id":42,"type":"temperature","location":"Kitchen","last_event_id":1}]`)
			} else {
				fmt.Fprint(w, `[]`)
			}
			return
		}
		count := polls.Add(1)
		if !healthy.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Query().Get("location") != "Kitchen" {
			t.Errorf("unexpected temperature URL: %s", r.URL)
		}
		fmt.Fprintf(w, `{"value":%g,"unit":"°C","location":"Kitchen","status":"active","timestamp":"2026-11-11T12:00:00Z"}`, 23.5+float64(count))
	}))
	defer upstream.Close()
	s := &server{db: pool, temperatureURL: upstream.URL, devicesURL: upstream.URL, client: upstream.Client()}
	created := deviceEvent{ID: 1, DeviceID: 42, Type: "device.created", Payload: device{ID: 42, Type: "temperature", Location: "Kitchen"}}
	for range 2 {
		if err := s.applyEvent(ctx, created); err != nil {
			t.Fatal(err)
		}
	}
	var processed int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM processed_events").Scan(&processed); err != nil || processed != 1 {
		t.Fatalf("deduplication: count=%d error=%v", processed, err)
	}
	snapshot := device{ID: 42, Type: "temperature", Location: "Kitchen", Version: 1}
	for i := range 3 {
		response := httptest.NewRecorder()
		s.latest(response, httptest.NewRequest(http.MethodGet, "/telemetry?device_ids=42", nil))
		var readings []measurement
		if err := json.Unmarshal(response.Body.Bytes(), &readings); err != nil || response.Code != 200 || len(readings) != 1 || readings[0].Value != 24.5+float64(i) || readings[0].DeviceID != 42 {
			t.Fatalf("stored reading: %d %s error=%v", response.Code, response.Body, err)
		}
	}
	if polls.Load() != 3 {
		t.Fatalf("got %d synchronous polls, want 3", polls.Load())
	}
	healthy.Store(false)
	failedResponse := httptest.NewRecorder()
	s.latest(failedResponse, httptest.NewRequest(http.MethodGet, "/telemetry?device_ids=42", nil))
	if failedResponse.Code != 502 {
		t.Fatalf("upstream outage returned cached success: %d %s", failedResponse.Code, failedResponse.Body)
	}
	healthy.Store(true)
	deleted := created
	deleted.ID = 3
	deleted.Type = "device.deleted"
	late := created
	late.ID = 2
	late.Type = "device.updated"
	late.Payload.Location = "Bedroom"
	for _, event := range []deviceEvent{deleted, late, created} {
		if err := s.applyEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	var version int64
	var removed bool
	if err := pool.QueryRow(ctx, "SELECT last_event_id, deleted_at IS NOT NULL FROM telemetry_devices WHERE device_id=42").Scan(&version, &removed); err != nil || version != 3 || !removed {
		t.Fatalf("late event resurrected device: %d %v %v", version, removed, err)
	}
	if err := s.pollDevice(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	var measurements int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM telemetry_data").Scan(&measurements); err != nil || measurements != 3 {
		t.Fatalf("stale poll stored data after deletion: %d %v", measurements, err)
	}
	active.Store(false)
	response := httptest.NewRecorder()
	s.latest(response, httptest.NewRequest(http.MethodGet, "/telemetry?device_ids=42", nil))
	if response.Code != 200 || response.Body.String() != "[]\n" {
		t.Fatalf("deleted reading exposed: %d %s", response.Code, response.Body)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE telemetry_devices ADD CONSTRAINT reject_device CHECK (device_id <> 99) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	failed := created
	failed.ID = 4
	failed.DeviceID = 99
	failed.Payload.ID = 99
	if err := s.applyEvent(ctx, failed); err == nil {
		t.Fatal("projection failure was ignored")
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM processed_events WHERE event_id=4").Scan(&processed); err != nil || processed != 0 {
		t.Fatalf("processed marker committed without projection: %d %v", processed, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE telemetry_devices DROP CONSTRAINT reject_device"); err != nil {
		t.Fatal(err)
	}
	if err := s.applyEvent(ctx, failed); err != nil {
		t.Fatalf("retry after rollback failed: %v", err)
	}
}
