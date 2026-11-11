// Сгенерировано нейросетью.
package main

import (
	"encoding/json"
	"log"
	"math/rand/v2"
	"net/http"
	"time"
)

var locations = map[string]string{
	"1": "Living Room",
	"2": "Bedroom",
	"3": "Kitchen",
}

type temperatureResponse struct {
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
	Timestamp  time.Time `json:"timestamp"`
	Location   string    `json:"location"`
	Status     string    `json:"status"`
	SensorID   string    `json:"sensor_id"`
	SensorType string    `json:"sensor_type"`
}

func temperature(w http.ResponseWriter, r *http.Request) {
	sensorID := r.PathValue("id")
	location := r.URL.Query().Get("location")
	if location == "" {
		location = locations[sensorID]
		if location == "" {
			location = "Unknown"
		}
	}
	if sensorID == "" {
		sensorID = "0"
		for id, room := range locations {
			if room == location {
				sensorID = id
				break
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(temperatureResponse{
		Value:      15 + rand.Float64()*15,
		Unit:       "°C",
		Timestamp:  time.Now().UTC(),
		Location:   location,
		Status:     "active",
		SensorID:   sensorID,
		SensorType: "temperature",
	}); err != nil {
		log.Printf("Failed to write temperature response: %v", err)
	}
}

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /temperature", temperature)
	mux.HandleFunc("GET /temperature/{id}", temperature)
	return mux
}

func main() {
	server := &http.Server{
		Addr:              ":8081",
		Handler:           routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
