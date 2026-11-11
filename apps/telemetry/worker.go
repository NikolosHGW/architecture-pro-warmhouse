package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

func (s *server) poll(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.collect(ctx); err != nil && ctx.Err() == nil {
			log.Printf("Telemetry polling: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) collect(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.db.Query(queryCtx, `SELECT device_id, type, location, last_event_id FROM telemetry_devices
		WHERE deleted_at IS NULL AND type = 'temperature' ORDER BY device_id`)
	if err != nil {
		return err
	}
	var devices []device
	for rows.Next() {
		var d device
		if err := rows.Scan(&d.ID, &d.Type, &d.Location, &d.Version); err != nil {
			rows.Close()
			return err
		}
		devices = append(devices, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range devices {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.pollDevice(ctx, d); err != nil && ctx.Err() == nil {
			log.Printf("Polling device %d: %v", d.ID, err)
		}
	}
	return nil
}

func (s *server) pollDevice(ctx context.Context, d device) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.temperatureURL+"/temperature?location="+url.QueryEscape(d.Location), nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("temperature API returned %d", response.StatusCode)
	}
	var m struct {
		Value     *float64  `json:"value"`
		Location  string    `json:"location"`
		Unit      string    `json:"unit"`
		Status    string    `json:"status"`
		Timestamp time.Time `json:"timestamp"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&m); err != nil {
		return err
	}
	if m.Value == nil || m.Location != d.Location || m.Unit != "°C" || m.Status != "active" || m.Timestamp.IsZero() {
		return fmt.Errorf("invalid temperature response for device %d", d.ID)
	}
	_, err = s.db.Exec(ctx, `INSERT INTO telemetry_data (device_id, metric, location, value, unit, received_at)
		SELECT device_id, 'temperature', location, $1, $2, $3 FROM telemetry_devices
		WHERE device_id = $4 AND last_event_id = $5 AND deleted_at IS NULL AND type = 'temperature'`,
		*m.Value, m.Unit, m.Timestamp, d.ID, d.Version)
	return err
}
