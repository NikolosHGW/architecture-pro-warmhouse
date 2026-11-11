package models

import "time"

const OwnerID = 1

type DeviceRegistration struct {
	TypeID           int            `json:"type_id"`
	Name             string         `json:"name"`
	Protocol         string         `json:"protocol"`
	LocationID       *int           `json:"location_id,omitempty"`
	Endpoint         string         `json:"endpoint,omitempty"`
	ConnectionConfig map[string]any `json:"connection_config,omitempty"`
}

type Device struct {
	ID         int       `json:"id"`
	OwnerID    int       `json:"owner_id"`
	TypeID     int       `json:"type_id"`
	LocationID int       `json:"location_id"`
	Name       string    `json:"name"`
	Protocol   string    `json:"protocol"`
	CreatedAt  time.Time `json:"created_at"`
}

// DeviceDetails is the private representation used by the legacy BFF adapter.
type DeviceDetails struct {
	Device
	Type             string         `json:"type"`
	Location         string         `json:"location"`
	Endpoint         string         `json:"-"`
	ConnectionConfig map[string]any `json:"-"`
	Value            float64        `json:"value"`
	Unit             string         `json:"unit"`
	Status           string         `json:"status"`
	LastUpdated      time.Time      `json:"last_updated"`
	Version          int64          `json:"last_event_id"`
}

type DeviceUpdate struct {
	Name       string   `json:"name"`
	TypeID     int      `json:"type_id"`
	LocationID int      `json:"location_id"`
	Value      *float64 `json:"value"`
	Unit       string   `json:"unit"`
	Status     string   `json:"status"`
}

type DeviceType struct {
	ID   int    `json:"id"`
	Code string `json:"code"`
}

type Location struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Message }
