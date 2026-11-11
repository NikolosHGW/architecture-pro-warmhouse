package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"devices/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegrationOutboxAtomicity(t *testing.T) {
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
	schema := pgx.Identifier{fmt.Sprintf("test_outbox_%d", time.Now().UnixNano())}.Sanitize()
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
	initSQL, err := os.ReadFile(filepath.Join("..", "init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(initSQL)); err != nil {
		t.Fatal(err)
	}
	database := &DB{Pool: pool}
	for _, input := range []models.DeviceRegistration{
		{Name: "Unknown", TypeID: 99, Protocol: "http"},
		{Name: "Unsupported", TypeID: 1, Protocol: "mqtt"},
		{Name: "Camera without endpoint", TypeID: 3, Protocol: "rtsp"},
	} {
		if _, err := database.RegisterDevice(ctx, input); err == nil {
			t.Fatalf("invalid registration accepted: %+v", input)
		}
	}
	var foreignLocation int
	if err := pool.QueryRow(ctx, "INSERT INTO locations(owner_id,name) VALUES(2,'Other owner') RETURNING id").Scan(&foreignLocation); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RegisterDevice(ctx, models.DeviceRegistration{Name: "Forbidden", TypeID: 1, Protocol: "http", LocationID: &foreignLocation}); err == nil {
		t.Fatal("foreign location accepted")
	}
	device, err := database.RegisterDevice(ctx, models.DeviceRegistration{Name: "Test", TypeID: 1, Protocol: "http"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateDevice(ctx, device.ID, models.DeviceUpdate{Name: "Updated device"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE outbox_devices ADD CONSTRAINT reject_delete CHECK (type <> 'device.deleted') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteDevice(ctx, device.ID); err == nil {
		t.Fatal("delete committed despite failed outbox insert")
	}
	if _, err := database.GetDevice(ctx, device.ID); err != nil {
		t.Fatalf("device deletion was not rolled back: %v", err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE outbox_devices DROP CONSTRAINT reject_delete"); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteDevice(ctx, device.ID); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	if err := pool.QueryRow(ctx, "SELECT array_agg(type ORDER BY id) FROM outbox_devices").Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(kinds) != "[device.created device.updated device.deleted]" {
		t.Fatalf("events: %v", kinds)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE outbox_devices ADD CONSTRAINT reject_create CHECK (type <> 'device.created') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RegisterDevice(ctx, models.DeviceRegistration{Name: "Rejected", TypeID: 1, Protocol: "http"}); err == nil {
		t.Fatal("create committed despite failed outbox insert")
	}
	var devices, events int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM devices WHERE deleted_at IS NULL), (SELECT count(*) FROM outbox_devices)").Scan(&devices, &events); err != nil {
		t.Fatal(err)
	}
	if devices != 0 || events != 3 {
		t.Fatalf("rollback failed: devices=%d events=%d", devices, events)
	}
}
