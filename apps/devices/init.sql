CREATE TABLE IF NOT EXISTS device_types (
    id BIGINT PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL
);
INSERT INTO device_types (id, code, name) VALUES
    (1, 'temperature', 'Датчик температуры'),
    (2, 'humidity', 'Датчик влажности'),
    (3, 'camera', 'Камера')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS locations (
    id BIGSERIAL PRIMARY KEY,
    owner_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_id, name)
);
INSERT INTO locations (owner_id, name) VALUES (1, 'Дом') ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS devices (
    id BIGSERIAL PRIMARY KEY,
    owner_id BIGINT NOT NULL,
    type_id BIGINT NOT NULL REFERENCES device_types(id),
    location_id BIGINT NOT NULL REFERENCES locations(id),
    name TEXT NOT NULL,
    protocol TEXT NOT NULL,
    endpoint TEXT NOT NULL DEFAULT '',
    connection_config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    -- Compatibility fields for the existing /sensors contract.
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    unit TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'inactive',
    last_updated TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS outbox_devices (
    id BIGSERIAL PRIMARY KEY,
    device_id BIGINT NOT NULL,
    type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_devices(id) WHERE published_at IS NULL;
