package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TemperatureService handles fetching temperature data from external API
type TemperatureService struct {
	BaseURL    string
	HTTPClient *http.Client
}

// TemperatureResponse represents the response from the temperature API
type TemperatureResponse struct {
	ID          int       `json:"id"`
	DeviceID    int       `json:"device_id"`
	Value       float64   `json:"value"`
	Unit        string    `json:"unit"`
	Timestamp   time.Time `json:"timestamp"`
	Location    string    `json:"location"`
	Status      string    `json:"status"`
	SensorID    string    `json:"sensor_id"`
	SensorType  string    `json:"sensor_type"`
	Description string    `json:"description"`
}

// NewTemperatureService creates a new temperature service
func NewTemperatureService(baseURL string) *TemperatureService {
	return &TemperatureService{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetTemperature fetches temperature data for a specific location
func (s *TemperatureService) GetTemperature(ctx context.Context, location string) (*TemperatureResponse, error) {
	var reading TemperatureResponse
	err := s.read(ctx, url.Values{"location": {location}}, &reading)
	return &reading, err
}

func (s *TemperatureService) GetTemperatures(ctx context.Context, ids []int) (map[int]TemperatureResponse, error) {
	result := make(map[int]TemperatureResponse)
	if len(ids) == 0 {
		return result, nil
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	var readings []TemperatureResponse
	if err := s.read(ctx, url.Values{"device_ids": {strings.Join(parts, ",")}}, &readings); err != nil {
		return nil, err
	}
	for _, reading := range readings {
		result[reading.DeviceID] = reading
	}
	return result, nil
}

func (s *TemperatureService) read(ctx context.Context, query url.Values, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/telemetry?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	response, err := s.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("error reading telemetry: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("telemetry returned status %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(output)
}
