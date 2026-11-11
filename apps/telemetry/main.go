package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type measurement struct {
	ID        int64     `json:"id"`
	DeviceID  int       `json:"device_id"`
	Metric    string    `json:"metric"`
	Location  string    `json:"location"`
	Value     float64   `json:"value"`
	Unit      string    `json:"unit"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

type server struct {
	db             *pgxpool.Pool
	temperatureURL string
	devicesURL     string
	client         *http.Client
}

const latestQuery = `SELECT DISTINCT ON (m.device_id) m.id, m.device_id, m.metric, m.location, m.value, m.unit, m.received_at
    FROM telemetry_data m JOIN telemetry_devices d ON d.device_id = m.device_id
    WHERE d.deleted_at IS NULL AND d.type = 'temperature' AND m.metric = 'temperature' AND m.location = d.location`

func (s *server) latest(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var readings any
	if ids, ok := query["device_ids"]; ok {
		if len(ids) != 1 {
			http.Error(w, "invalid device_ids", http.StatusBadRequest)
			return
		}
		parsed, err := parseDeviceIDs(ids[0])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		parsed, err = s.refresh(r.Context(), parsed, "")
		if err != nil {
			log.Print(err)
			http.Error(w, "failed to obtain fresh telemetry", http.StatusBadGateway)
			return
		}
		rows, err := s.db.Query(r.Context(), latestQuery+` AND m.device_id = ANY($1) ORDER BY m.device_id, m.received_at DESC, m.id DESC`, parsed)
		if err != nil {
			log.Print(err)
			http.Error(w, "failed to read telemetry", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		result := []measurement{}
		for rows.Next() {
			var m measurement
			if err := rows.Scan(&m.ID, &m.DeviceID, &m.Metric, &m.Location, &m.Value, &m.Unit, &m.Timestamp); err != nil {
				log.Print(err)
				http.Error(w, "failed to read telemetry", http.StatusInternalServerError)
				return
			}
			m.Status = "active"
			result = append(result, m)
		}
		if err := rows.Err(); err != nil {
			log.Print(err)
			http.Error(w, "failed to read telemetry", http.StatusInternalServerError)
			return
		}
		readings = result
	} else {
		location := strings.TrimSpace(query.Get("location"))
		if location == "" {
			http.Error(w, "location is required", http.StatusBadRequest)
			return
		}
		ids, err := s.refresh(r.Context(), nil, location)
		if err != nil {
			log.Print(err)
			http.Error(w, "failed to obtain fresh telemetry", http.StatusBadGateway)
			return
		}
		var m measurement
		err = s.db.QueryRow(r.Context(), `SELECT m.id, m.device_id, m.metric, m.location, m.value, m.unit, m.received_at
            FROM telemetry_data m JOIN telemetry_devices d ON d.device_id = m.device_id
            WHERE d.deleted_at IS NULL AND d.type = 'temperature' AND m.metric = 'temperature'
            AND m.location = d.location AND m.location = $1 AND m.device_id = ANY($2)
            ORDER BY m.received_at DESC, m.id DESC LIMIT 1`, location, ids).
			Scan(&m.ID, &m.DeviceID, &m.Metric, &m.Location, &m.Value, &m.Unit, &m.Timestamp)
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "no measurement yet", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Print(err)
			http.Error(w, "failed to read telemetry", http.StatusInternalServerError)
			return
		}
		m.Status = "active"
		readings = m
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(readings); err != nil {
		log.Print(err)
	}
}

func parseDeviceIDs(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid device_ids")
		}
		ids = append(ids, int(id))
	}
	return ids, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	s := &server{db: pool, temperatureURL: os.Getenv("TEMPERATURE_API_URL"), devicesURL: os.Getenv("DEVICES_API_URL"), client: &http.Client{Timeout: 5 * time.Second}}
	go s.consumeEvents(ctx, os.Getenv("RABBITMQ_URL"))
	// Polling is retained in worker.go; reads currently collect measurements synchronously.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /telemetry", s.latest)
	mux.HandleFunc("GET /api/v1/telemetry", s.latest)
	mux.HandleFunc("GET /temperature", s.latest)
	httpServer := &http.Server{Addr: ":8083", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Print(err)
	}
}
