package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (s *server) refresh(ctx context.Context, ids []int, location string) ([]int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.devicesURL+"/internal/v1/devices", nil)
	if err != nil {
		return nil, err
	}
	response, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("devices returned %d", response.StatusCode)
	}
	var devices []device
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&devices); err != nil {
		return nil, err
	}
	wanted := make(map[int]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	fresh := []int{}
	for _, d := range devices {
		if d.Type != "temperature" || (location != "" && d.Location != location) || (location == "" && !wanted[d.ID]) {
			continue
		}
		if d.ID <= 0 || d.Version <= 0 || d.Location == "" {
			return nil, fmt.Errorf("invalid device configuration for %d", d.ID)
		}
		// The real outbox ID lets a later RabbitMQ delivery be safely deduplicated.
		if err := s.applyEvent(ctx, deviceEvent{ID: d.Version, DeviceID: d.ID, Type: "device.updated", Payload: d}); err != nil {
			return nil, err
		}
		if err := s.pollDevice(ctx, d); err != nil {
			return nil, err
		}
		fresh = append(fresh, d.ID)
	}
	return fresh, nil
}
