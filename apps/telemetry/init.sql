CREATE TABLE IF NOT EXISTS telemetry_devices (
    device_id BIGINT PRIMARY KEY,
    type TEXT NOT NULL,
    location TEXT NOT NULL,
    last_event_id BIGINT NOT NULL,
    deleted_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS processed_events (
    event_id BIGINT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS telemetry_data (
    id BIGSERIAL PRIMARY KEY,
    device_id BIGINT NOT NULL REFERENCES telemetry_devices(device_id),
    metric TEXT NOT NULL,
    location TEXT NOT NULL,
    value DOUBLE PRECISION NOT NULL,
    unit TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_telemetry_latest ON telemetry_data(device_id, metric, received_at DESC, id DESC);
