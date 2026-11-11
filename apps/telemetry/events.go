package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const eventsQueue = "telemetry.devices"

type device struct {
	ID       int    `json:"id"`
	Type     string `json:"type"`
	Location string `json:"location"`
	Version  int64  `json:"last_event_id,omitempty"`
}

type deviceEvent struct {
	ID       int64  `json:"id"`
	DeviceID int    `json:"device_id"`
	Type     string `json:"type"`
	Payload  device `json:"payload"`
}

func decodeEvent(body []byte) (deviceEvent, error) {
	var event deviceEvent
	if len(body) > 4096 {
		return event, errors.New("device event too large")
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return event, err
	}
	if event.ID <= 0 || event.DeviceID <= 0 || event.Payload.ID != event.DeviceID ||
		strings.TrimSpace(event.Payload.Location) == "" || strings.TrimSpace(event.Payload.Type) == "" {
		return event, errors.New("invalid device event payload")
	}
	switch event.Type {
	case "device.created", "device.updated", "device.deleted":
		return event, nil
	default:
		return event, fmt.Errorf("unknown device event type: %s", event.Type)
	}
}

func (s *server) applyEvent(ctx context.Context, event deviceEvent) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	inserted, err := tx.Exec(ctx, "INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING", event.ID)
	if err != nil || inserted.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO telemetry_devices (device_id, type, location, last_event_id, deleted_at)
		VALUES ($1, $2, $3, $4, CASE WHEN $5 THEN NOW() ELSE NULL END)
		ON CONFLICT (device_id) DO UPDATE SET type = EXCLUDED.type, location = EXCLUDED.location,
		last_event_id = EXCLUDED.last_event_id, deleted_at = EXCLUDED.deleted_at
		WHERE telemetry_devices.last_event_id < EXCLUDED.last_event_id`,
		event.DeviceID, event.Payload.Type, event.Payload.Location, event.ID, event.Type == "device.deleted")
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *server) consumeEvents(ctx context.Context, brokerURL string) {
	for ctx.Err() == nil {
		if err := s.consumeSession(ctx, brokerURL); err != nil && ctx.Err() == nil {
			log.Printf("Device consumer: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (s *server) consumeSession(ctx context.Context, brokerURL string) error {
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
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.ConsumeWithContext(ctx, eventsQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for delivery := range deliveries {
		event, err := decodeEvent(delivery.Body)
		if err != nil {
			log.Printf("Invalid device event moved to %s.failed: %v", eventsQueue, err)
			if err := delivery.Nack(false, false); err != nil {
				return err
			}
			continue
		}
		applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = s.applyEvent(applyCtx, event)
		cancel()
		if err != nil {
			return err // Closing the connection requeues this unacknowledged delivery.
		}
		if err := delivery.Ack(false); err != nil {
			return err
		}
	}
	return errors.New("RabbitMQ deliveries closed")
}
