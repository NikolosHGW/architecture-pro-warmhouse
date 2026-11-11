package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"smarthome/models"
)

const devicesAPI = "/api/v1/devices"
const internalDevicesAPI = "/internal/v1/devices"

type DeviceService struct {
	BaseURL    string
	HTTPClient *http.Client
}
type ServiceError struct {
	StatusCode int
	Message    string
}

func (e *ServiceError) Error() string { return e.Message }
func NewDeviceService(baseURL string) *DeviceService {
	return &DeviceService{BaseURL: baseURL, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
}

type deviceResponse struct {
	ID          int               `json:"id"`
	OwnerID     int               `json:"owner_id"`
	TypeID      int               `json:"type_id"`
	LocationID  int               `json:"location_id"`
	Name        string            `json:"name"`
	Protocol    string            `json:"protocol"`
	CreatedAt   time.Time         `json:"created_at"`
	Type        models.SensorType `json:"type"`
	Location    string            `json:"location"`
	Value       float64           `json:"value"`
	Unit        string            `json:"unit"`
	Status      string            `json:"status"`
	LastUpdated time.Time         `json:"last_updated"`
}

func (d deviceResponse) sensor() models.Sensor {
	return models.Sensor{ID: d.ID, Name: d.Name, Type: d.Type, Location: d.Location, Value: d.Value, Unit: d.Unit, Status: d.Status, CreatedAt: d.CreatedAt, LastUpdated: d.LastUpdated}
}

type deviceRegistration struct {
	TypeID           int            `json:"type_id"`
	LocationID       int            `json:"location_id"`
	Name             string         `json:"name"`
	Protocol         string         `json:"protocol"`
	ConnectionConfig map[string]any `json:"connection_config"`
}

type deviceUpdate struct {
	Name       string   `json:"name,omitempty"`
	TypeID     int      `json:"type_id,omitempty"`
	LocationID int      `json:"location_id,omitempty"`
	Value      *float64 `json:"value,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	Status     string   `json:"status,omitempty"`
}

func (s *DeviceService) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.BaseURL+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := s.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("devices unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var message struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(response.Body).Decode(&message); err != nil || message.Message == "" {
			message.Message = response.Status
		}
		if message.Code == "device_not_found" {
			message.Message = "Sensor not found"
		}
		return &ServiceError{StatusCode: response.StatusCode, Message: message.Message}
	}
	if output == nil {
		_, err := io.Copy(io.Discard, response.Body)
		return err
	}
	return json.NewDecoder(response.Body).Decode(output)
}

func (s *DeviceService) typeID(ctx context.Context, kind models.SensorType) (int, error) {
	var result struct {
		ID int `json:"id"`
	}
	err := s.request(ctx, http.MethodGet, "/internal/v1/device-types?code="+url.QueryEscape(string(kind)), nil, &result)
	if remote, ok := err.(*ServiceError); ok && remote.StatusCode == 404 {
		return 0, &ServiceError{StatusCode: 422, Message: "Unknown sensor type"}
	}
	return result.ID, err
}
func (s *DeviceService) locationID(ctx context.Context, name string) (int, error) {
	var result struct {
		ID int `json:"id"`
	}
	err := s.request(ctx, http.MethodPost, "/internal/v1/locations", struct {
		Name string `json:"name"`
	}{name}, &result)
	return result.ID, err
}
func (s *DeviceService) GetSensors(ctx context.Context) ([]models.Sensor, error) {
	var devices []deviceResponse
	if err := s.request(ctx, http.MethodGet, internalDevicesAPI, nil, &devices); err != nil {
		return nil, err
	}
	var sensors []models.Sensor
	for _, d := range devices {
		sensors = append(sensors, d.sensor())
	}
	return sensors, nil
}
func (s *DeviceService) GetSensorByID(ctx context.Context, id int) (models.Sensor, error) {
	var d deviceResponse
	err := s.request(ctx, http.MethodGet, internalDevicesAPI+"/"+strconv.Itoa(id), nil, &d)
	return d.sensor(), err
}
func (s *DeviceService) CreateSensor(ctx context.Context, input models.SensorCreate) (models.Sensor, error) {
	typeID, err := s.typeID(ctx, input.Type)
	if err != nil {
		return models.Sensor{}, err
	}
	locationID, err := s.locationID(ctx, input.Location)
	if err != nil {
		return models.Sensor{}, err
	}
	registration := deviceRegistration{TypeID: typeID, LocationID: locationID, Name: input.Name, Protocol: "http", ConnectionConfig: map[string]any{"unit": input.Unit}}
	var d deviceResponse
	if err := s.request(ctx, http.MethodPost, devicesAPI, registration, &d); err != nil {
		return models.Sensor{}, err
	}
	d.Type = input.Type
	d.Location = input.Location
	d.Unit = input.Unit
	d.Status = "inactive"
	d.LastUpdated = d.CreatedAt
	return d.sensor(), nil
}
func (s *DeviceService) UpdateSensor(ctx context.Context, id int, input models.SensorUpdate) (models.Sensor, error) {
	update := deviceUpdate{Name: input.Name, Value: input.Value, Unit: input.Unit, Status: input.Status}
	var err error
	if input.Type != "" {
		update.TypeID, err = s.typeID(ctx, input.Type)
		if err != nil {
			return models.Sensor{}, err
		}
	}
	if input.Location != "" {
		update.LocationID, err = s.locationID(ctx, input.Location)
		if err != nil {
			return models.Sensor{}, err
		}
	}
	var d deviceResponse
	err = s.request(ctx, http.MethodPut, internalDevicesAPI+"/"+strconv.Itoa(id), update, &d)
	return d.sensor(), err
}
func (s *DeviceService) DeleteSensor(ctx context.Context, id int) error {
	return s.request(ctx, http.MethodDelete, devicesAPI+"/"+strconv.Itoa(id), nil, nil)
}
func (s *DeviceService) UpdateSensorValue(ctx context.Context, id int, value float64, status string) error {
	return s.request(ctx, http.MethodPatch, internalDevicesAPI+"/"+strconv.Itoa(id)+"/value", deviceUpdate{Value: &value, Status: status}, nil)
}
