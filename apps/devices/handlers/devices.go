package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"devices/db"
	"devices/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type DeviceHandler struct{ DB *db.DB }

func NewDeviceHandler(database *db.DB) *DeviceHandler { return &DeviceHandler{DB: database} }

func (h *DeviceHandler) RegisterRoutes(router *gin.RouterGroup) {
	devices := router.Group("/devices")
	devices.POST("", h.RegisterDevice)
	devices.DELETE("/:device_id", h.DeleteDevice)
}
func (h *DeviceHandler) RegisterInternalRoutes(router *gin.RouterGroup) {
	devices := router.Group("/devices")
	devices.GET("", h.ListDevices)
	devices.GET("/:device_id", h.GetDevice)
	devices.PUT("/:device_id", h.UpdateDevice)
	devices.PATCH("/:device_id/value", h.UpdateDevice)
	devices.GET("/:device_id/camera-config", h.GetCameraConfig)
	router.GET("/device-types", h.GetDeviceType)
	router.POST("/locations", h.ResolveLocation)
}

func fail(c *gin.Context, err error) {
	var api *models.APIError
	if errors.As(err, &api) {
		c.JSON(api.Status, api)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, models.APIError{Code: "device_not_found", Message: "Device not found"})
		return
	}
	log.Print(err)
	c.JSON(http.StatusInternalServerError, models.APIError{Code: "internal_error", Message: "Device operation failed"})
}
func badRequest(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, models.APIError{Code: "invalid_request", Message: message})
}

func decode(c *gin.Context, target any) bool {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	if err != nil {
		badRequest(c, "Invalid request body")
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		badRequest(c, "Request must contain one JSON object")
		return false
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			badRequest(c, "Fields cannot be null")
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		badRequest(c, "Invalid JSON: "+err.Error())
		return false
	}
	return true
}
func deviceID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("device_id"))
	if err != nil || id < 1 {
		badRequest(c, "Invalid device ID")
		return 0, false
	}
	return id, true
}

func (h *DeviceHandler) RegisterDevice(c *gin.Context) {
	var input models.DeviceRegistration
	if !decode(c, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Protocol = strings.TrimSpace(input.Protocol)
	if input.Name == "" || input.TypeID < 1 || input.Protocol == "" || (input.LocationID != nil && *input.LocationID < 1) {
		badRequest(c, "type_id, name and protocol are required; IDs must be positive")
		return
	}
	device, err := h.DB.RegisterDevice(c.Request.Context(), input)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, device.Device)
}
func (h *DeviceHandler) DeleteDevice(c *gin.Context) {
	id, ok := deviceID(c)
	if !ok {
		return
	}
	if err := h.DB.DeleteDevice(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *DeviceHandler) ListDevices(c *gin.Context) {
	devices, err := h.DB.ListDevices(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, devices)
}
func (h *DeviceHandler) GetDevice(c *gin.Context) {
	id, ok := deviceID(c)
	if !ok {
		return
	}
	device, err := h.DB.GetDevice(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, device)
}
func (h *DeviceHandler) UpdateDevice(c *gin.Context) {
	id, ok := deviceID(c)
	if !ok {
		return
	}
	var input models.DeviceUpdate
	if !decode(c, &input) {
		return
	}
	if input.TypeID < 0 || input.LocationID < 0 || (input.Name != "" && strings.TrimSpace(input.Name) == "") {
		badRequest(c, "Invalid device update")
		return
	}
	if c.Request.Method == http.MethodPatch && (input.Value == nil || input.Status == "") {
		badRequest(c, "value and status are required")
		return
	}
	device, err := h.DB.UpdateDevice(c.Request.Context(), id, input)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, device)
}
func (h *DeviceHandler) GetCameraConfig(c *gin.Context) {
	id, ok := deviceID(c)
	if !ok {
		return
	}
	device, err := h.DB.GetDevice(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	if device.Type != "camera" || device.Endpoint == "" {
		fail(c, &models.APIError{Status: 422, Code: "invalid_camera", Message: "Device is not a configured camera"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"device_id": device.ID, "owner_id": device.OwnerID, "endpoint": device.Endpoint,
		"protocol": device.Protocol, "connection_config": device.ConnectionConfig})
}
func (h *DeviceHandler) GetDeviceType(c *gin.Context) {
	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		badRequest(c, "code is required")
		return
	}
	kind, err := h.DB.FindDeviceType(c.Request.Context(), code)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, kind)
}
func (h *DeviceHandler) ResolveLocation(c *gin.Context) {
	var input struct {
		Name string `json:"name"`
	}
	if !decode(c, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		badRequest(c, "name is required")
		return
	}
	location, err := h.DB.ResolveLocation(c.Request.Context(), input.Name)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, location)
}
