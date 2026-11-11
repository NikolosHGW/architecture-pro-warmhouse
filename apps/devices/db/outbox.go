package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"devices/models"

	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"
)

const eventsQueue = "telemetry.devices"

type deviceEvent struct {
	ID       int64           `json:"id"`
	DeviceID int             `json:"device_id"`
	Type     string          `json:"type"`
	Payload  json.RawMessage `json:"payload"`
}

func enqueueEvent(ctx context.Context, tx pgx.Tx, kind string, device models.DeviceDetails) error {
	_, err := tx.Exec(ctx, `INSERT INTO outbox_devices (device_id, type, payload)
		VALUES ($1, $2, jsonb_build_object('id', $1::bigint, 'type', $3::text, 'location', $4::text))`,
		device.ID, kind, device.Type, device.Location)
	return err
}

func (db *DB) PublishOutbox(ctx context.Context, brokerURL string) {
	for ctx.Err() == nil {
		if err := db.publishSession(ctx, brokerURL); err != nil && ctx.Err() == nil {
			log.Printf("Outbox publisher: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (db *DB) publishSession(ctx context.Context, brokerURL string) error {
	conn, err := amqp.DialConfig(brokerURL, amqp.Config{Heartbeat: 10 * time.Second, Dial: amqp.DefaultDial(5 * time.Second)})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(eventsQueue+".failed", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(eventsQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": "", "x-dead-letter-routing-key": eventsQueue + ".failed",
	}); err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}
	returned := ch.NotifyReturn(make(chan amqp.Return, 1))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ch.IsClosed() {
			return errors.New("RabbitMQ channel closed")
		}
		for range 100 {
			publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			found, err := db.publishOne(publishCtx, ch, returned)
			cancel()
			if err != nil {
				return err
			}
			if !found {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (db *DB) publishOne(ctx context.Context, ch *amqp.Channel, returned <-chan amqp.Return) (bool, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var event deviceEvent
	err = tx.QueryRow(ctx, `SELECT id, device_id, type, payload FROM outbox_devices
		WHERE published_at IS NULL ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).
		Scan(&event.ID, &event.DeviceID, &event.Type, &event.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return false, err
	}
	confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", eventsQueue, true, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent,
		MessageId: strconv.FormatInt(event.ID, 10), Body: body,
	})
	if err != nil {
		return false, err
	}
	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		return false, err
	}
	if !acked {
		return false, errors.New("RabbitMQ did not confirm event")
	}
	select {
	case message := <-returned:
		return false, fmt.Errorf("unrouted event: %s", message.ReplyText)
	default:
	}
	if _, err := tx.Exec(ctx, "UPDATE outbox_devices SET published_at = NOW() WHERE id = $1", event.ID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
