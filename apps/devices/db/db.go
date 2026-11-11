package db

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"devices/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct{ Pool *pgxpool.Pool }

func New(connString string) (*DB, error) {
	pool, err := pgxpool.New(context.Background(), connString)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{Pool: pool}, nil
}
func (db *DB) Close() { db.Pool.Close() }

const deviceQuery = `SELECT d.id,d.owner_id,d.type_id,d.location_id,d.name,d.protocol,d.created_at,
    t.code,l.name,d.endpoint,d.connection_config,d.value,d.unit,d.status,d.last_updated,
    COALESCE((SELECT MAX(o.id) FROM outbox_devices o WHERE o.device_id=d.id),0)
    FROM devices d JOIN device_types t ON t.id=d.type_id JOIN locations l ON l.id=d.location_id`

func scanDevice(row pgx.Row) (models.DeviceDetails, error) {
	var d models.DeviceDetails
	err := row.Scan(&d.ID, &d.OwnerID, &d.TypeID, &d.LocationID, &d.Name, &d.Protocol, &d.CreatedAt,
		&d.Type, &d.Location, &d.Endpoint, &d.ConnectionConfig, &d.Value, &d.Unit, &d.Status, &d.LastUpdated, &d.Version)
	return d, err
}

func (db *DB) ListDevices(ctx context.Context) ([]models.DeviceDetails, error) {
	rows, err := db.Pool.Query(ctx, deviceQuery+" WHERE d.owner_id=$1 AND d.deleted_at IS NULL ORDER BY d.id", models.OwnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := []models.DeviceDetails{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}
func (db *DB) GetDevice(ctx context.Context, id int) (models.DeviceDetails, error) {
	return scanDevice(db.Pool.QueryRow(ctx, deviceQuery+" WHERE d.id=$1 AND d.owner_id=$2 AND d.deleted_at IS NULL", id, models.OwnerID))
}

func invalidDevice(message string) error {
	return &models.APIError{Status: http.StatusUnprocessableEntity, Code: "invalid_device", Message: message}
}

func validateConfiguration(kind, protocol, endpoint string, config map[string]any) error {
	if protocol != "http" && protocol != "rtsp" {
		return invalidDevice("Unsupported protocol")
	}
	if protocol == "rtsp" && kind != "camera" {
		return invalidDevice("RTSP is supported only for cameras")
	}
	if kind == "camera" && endpoint == "" {
		return invalidDevice("Camera endpoint is required")
	}
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			return invalidDevice("Invalid endpoint")
		}
		if protocol == "http" && parsed.Scheme != "http" && parsed.Scheme != "https" {
			return invalidDevice("HTTP endpoint is required")
		}
		if protocol == "rtsp" && parsed.Scheme != "rtsp" && parsed.Scheme != "rtsps" {
			return invalidDevice("RTSP endpoint is required")
		}
	}
	if unit, exists := config["unit"]; exists {
		if _, ok := unit.(string); !ok {
			return invalidDevice("unit must be a string")
		}
	}
	return nil
}

func (db *DB) configuration(ctx context.Context, tx pgx.Tx, typeID, locationID int) (string, error) {
	var kind string
	if err := tx.QueryRow(ctx, "SELECT code FROM device_types WHERE id=$1", typeID).Scan(&kind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", invalidDevice("Unknown device type")
		}
		return "", err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM locations WHERE id=$1 AND owner_id=$2)", locationID, models.OwnerID).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		return "", invalidDevice("Location is unavailable to this owner")
	}
	return kind, nil
}

func (db *DB) RegisterDevice(ctx context.Context, input models.DeviceRegistration) (models.DeviceDetails, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return models.DeviceDetails{}, err
	}
	defer tx.Rollback(ctx)
	locationID := 1
	if input.LocationID != nil {
		locationID = *input.LocationID
	}
	kind, err := db.configuration(ctx, tx, input.TypeID, locationID)
	if err != nil {
		return models.DeviceDetails{}, err
	}
	if err := validateConfiguration(kind, input.Protocol, input.Endpoint, input.ConnectionConfig); err != nil {
		return models.DeviceDetails{}, err
	}
	if input.ConnectionConfig == nil {
		input.ConnectionConfig = map[string]any{}
	}
	unit := ""
	if kind == "temperature" {
		unit = "°C"
	}
	if value, ok := input.ConnectionConfig["unit"].(string); ok {
		unit = value
	}
	config, err := json.Marshal(input.ConnectionConfig)
	if err != nil {
		return models.DeviceDetails{}, err
	}
	var id int
	err = tx.QueryRow(ctx, `INSERT INTO devices(owner_id,type_id,location_id,name,protocol,endpoint,connection_config,unit)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, models.OwnerID, input.TypeID, locationID, input.Name, input.Protocol, input.Endpoint, config, unit).Scan(&id)
	if err != nil {
		return models.DeviceDetails{}, err
	}
	d, err := scanDevice(tx.QueryRow(ctx, deviceQuery+" WHERE d.id=$1", id))
	if err != nil {
		return models.DeviceDetails{}, err
	}
	if err := enqueueEvent(ctx, tx, "device.created", d); err != nil {
		return models.DeviceDetails{}, err
	}
	return d, tx.Commit(ctx)
}

func (db *DB) UpdateDevice(ctx context.Context, id int, input models.DeviceUpdate) (models.DeviceDetails, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return models.DeviceDetails{}, err
	}
	defer tx.Rollback(ctx)
	d, err := scanDevice(tx.QueryRow(ctx, deviceQuery+" WHERE d.id=$1 AND d.owner_id=$2 AND d.deleted_at IS NULL FOR UPDATE OF d", id, models.OwnerID))
	if err != nil {
		return models.DeviceDetails{}, err
	}
	if input.Name != "" {
		d.Name = input.Name
	}
	if input.TypeID != 0 {
		d.TypeID = input.TypeID
	}
	if input.LocationID != 0 {
		d.LocationID = input.LocationID
	}
	if input.Value != nil {
		d.Value = *input.Value
	}
	if input.Unit != "" {
		d.Unit = input.Unit
	}
	if input.Status != "" {
		d.Status = input.Status
	}
	kind, err := db.configuration(ctx, tx, d.TypeID, d.LocationID)
	if err != nil {
		return models.DeviceDetails{}, err
	}
	if err := validateConfiguration(kind, d.Protocol, d.Endpoint, d.ConnectionConfig); err != nil {
		return models.DeviceDetails{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE devices SET name=$1,type_id=$2,location_id=$3,value=$4,unit=$5,status=$6,last_updated=NOW() WHERE id=$7`,
		d.Name, d.TypeID, d.LocationID, d.Value, d.Unit, d.Status, id)
	if err != nil {
		return models.DeviceDetails{}, err
	}
	d, err = scanDevice(tx.QueryRow(ctx, deviceQuery+" WHERE d.id=$1", id))
	if err != nil {
		return models.DeviceDetails{}, err
	}
	if err := enqueueEvent(ctx, tx, "device.updated", d); err != nil {
		return models.DeviceDetails{}, err
	}
	return d, tx.Commit(ctx)
}

func (db *DB) DeleteDevice(ctx context.Context, id int) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	d, err := scanDevice(tx.QueryRow(ctx, deviceQuery+" WHERE d.id=$1 AND d.owner_id=$2 AND d.deleted_at IS NULL FOR UPDATE OF d", id, models.OwnerID))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE devices SET deleted_at=NOW() WHERE id=$1", id); err != nil {
		return err
	}
	if err := enqueueEvent(ctx, tx, "device.deleted", d); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (db *DB) FindDeviceType(ctx context.Context, code string) (models.DeviceType, error) {
	var result models.DeviceType
	err := db.Pool.QueryRow(ctx, "SELECT id,code FROM device_types WHERE code=$1", code).Scan(&result.ID, &result.Code)
	return result, err
}
func (db *DB) ResolveLocation(ctx context.Context, name string) (models.Location, error) {
	var location models.Location
	err := db.Pool.QueryRow(ctx, `INSERT INTO locations(owner_id,name) VALUES($1,$2)
        ON CONFLICT(owner_id,name) DO UPDATE SET name=EXCLUDED.name RETURNING id,name`, models.OwnerID, name).Scan(&location.ID, &location.Name)
	return location, err
}
