package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"smarthome/models"
	"smarthome/services"

	"github.com/gin-gonic/gin"
)

func writeServiceError(c *gin.Context, err error) {
	status := http.StatusBadGateway
	var remote *services.ServiceError
	if errors.As(err, &remote) {
		status = remote.StatusCode
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

func (h *SensorHandler) GetTelemetry(c *gin.Context) {
	location := strings.TrimSpace(c.Query("location"))
	if location == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "location is required"})
		return
	}
	data, err := h.TemperatureService.GetTemperature(c.Request.Context(), location)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"id": data.ID, "location": data.Location, "value": data.Value,
		"unit": data.Unit, "status": data.Status, "timestamp": data.Timestamp,
	})
}

// SensorHandler handles sensor-related requests
type SensorHandler struct {
	DeviceService      *services.DeviceService
	TemperatureService *services.TemperatureService
}

// NewSensorHandler creates a new SensorHandler
func NewSensorHandler(deviceService *services.DeviceService, temperatureService *services.TemperatureService) *SensorHandler {
	return &SensorHandler{
		DeviceService:      deviceService,
		TemperatureService: temperatureService,
	}
}

// RegisterRoutes registers the sensor routes
func (h *SensorHandler) RegisterRoutes(router *gin.RouterGroup) {
	sensors := router.Group("/sensors")
	{
		sensors.GET("", h.GetSensors)
		sensors.GET("/:id", h.GetSensorByID)
		sensors.POST("", h.CreateSensor)
		sensors.PUT("/:id", h.UpdateSensor)
		sensors.DELETE("/:id", h.DeleteSensor)
		sensors.PATCH("/:id/value", h.UpdateSensorValue)
		sensors.GET("/temperature/:location", h.GetTemperatureByLocation)
	}
}

// GetSensors handles GET /api/v1/sensors
func (h *SensorHandler) GetSensors(c *gin.Context) {
	sensors, err := h.DeviceService.GetSensors(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}

	ids := []int{}
	for _, sensor := range sensors {
		if sensor.Type == models.Temperature {
			ids = append(ids, sensor.ID)
		}
	}
	readings, err := h.TemperatureService.GetTemperatures(c.Request.Context(), ids)
	if err != nil {
		log.Printf("Failed to read telemetry: %v", err)
	}
	for i := range sensors {
		if reading, ok := readings[sensors[i].ID]; ok {
			sensors[i].Value = reading.Value
			sensors[i].Status = reading.Status
			sensors[i].LastUpdated = reading.Timestamp
		}
	}

	c.JSON(http.StatusOK, sensors)
}

// GetSensorByID handles GET /api/v1/sensors/:id
func (h *SensorHandler) GetSensorByID(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sensor ID"})
		return
	}

	sensor, err := h.DeviceService.GetSensorByID(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	if sensor.Type == models.Temperature {
		readings, err := h.TemperatureService.GetTemperatures(c.Request.Context(), []int{sensor.ID})
		if err != nil {
			log.Printf("Failed to read telemetry for sensor %d: %v", sensor.ID, err)
		}
		if reading, ok := readings[sensor.ID]; ok {
			sensor.Value = reading.Value
			sensor.Status = reading.Status
			sensor.LastUpdated = reading.Timestamp
		}
	}

	c.JSON(http.StatusOK, sensor)
}

// GetTemperatureByLocation handles GET /api/v1/sensors/temperature/:location
func (h *SensorHandler) GetTemperatureByLocation(c *gin.Context) {
	location := c.Param("location")
	if location == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Location is required"})
		return
	}

	// Fetch temperature data from the external API
	tempData, err := h.TemperatureService.GetTemperature(c.Request.Context(), location)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to fetch temperature data: %v", err),
		})
		return
	}

	// Return the temperature data
	c.JSON(http.StatusOK, gin.H{
		"location":    tempData.Location,
		"value":       tempData.Value,
		"unit":        tempData.Unit,
		"status":      tempData.Status,
		"timestamp":   tempData.Timestamp,
		"description": tempData.Description,
	})
}

// CreateSensor handles POST /api/v1/sensors
func (h *SensorHandler) CreateSensor(c *gin.Context) {
	var sensorCreate models.SensorCreate
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := c.ShouldBindJSON(&sensorCreate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sensorCreate.Name = strings.TrimSpace(sensorCreate.Name)
	sensorCreate.Location = strings.TrimSpace(sensorCreate.Location)
	if sensorCreate.Name == "" || sensorCreate.Location == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and location are required"})
		return
	}

	sensor, err := h.DeviceService.CreateSensor(c.Request.Context(), sensorCreate)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusCreated, sensor)
}

// UpdateSensor handles PUT /api/v1/sensors/:id
func (h *SensorHandler) UpdateSensor(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sensor ID"})
		return
	}

	var sensorUpdate models.SensorUpdate
	if err := c.ShouldBindJSON(&sensorUpdate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sensor, err := h.DeviceService.UpdateSensor(c.Request.Context(), id, sensorUpdate)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, sensor)
}

// DeleteSensor handles DELETE /api/v1/sensors/:id
func (h *SensorHandler) DeleteSensor(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sensor ID"})
		return
	}

	err = h.DeviceService.DeleteSensor(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Sensor deleted successfully"})
}

// UpdateSensorValue handles PATCH /api/v1/sensors/:id/value
func (h *SensorHandler) UpdateSensorValue(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sensor ID"})
		return
	}

	var request struct {
		Value  float64 `json:"value" binding:"required"`
		Status string  `json:"status" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err = h.DeviceService.UpdateSensorValue(c.Request.Context(), id, request.Value, request.Status)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Sensor value updated successfully"})
}
