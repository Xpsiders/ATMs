package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/httpx"
	"attendance-api/internal/scans"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
)

type DeviceStore interface {
	LoadDeviceByKey(context.Context, []byte) (scans.Device, bool, error)
	TouchDeviceHeartbeat(context.Context, scans.Device, time.Time) error
	RecordDeviceStatus(context.Context, scans.Device, scans.DeviceStatus, time.Time) error
	SyncDeviceTemplates(context.Context, scans.Device, string) (scans.TemplateSync, error)
	scans.TaskStore
}

type Clock interface {
	Now() time.Time
}

type deviceContextKey struct{}

func DeviceFromContext(ctx context.Context) (scans.Device, bool) {
	device, ok := ctx.Value(deviceContextKey{}).(scans.Device)
	return device, ok
}

func DeviceAuth(store DeviceStore, clock Clock) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get("X-Device-Key"))
			if key == "" {
				httpx.WriteError(w, http.StatusUnauthorized, "device_auth_required", "A device key is required", nil)
				return
			}
			device, found, err := store.LoadDeviceByKey(r.Context(), scans.HashDeviceKey(key))
			if err != nil {
				slog.ErrorContext(r.Context(), "device authentication lookup failed", "error", err)
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to authenticate device", nil)
				return
			}
			if !found {
				slog.WarnContext(r.Context(), "device authentication rejected")
				httpx.WriteError(w, http.StatusUnauthorized, "invalid_device_key", "Device key is invalid or revoked", nil)
				return
			}
			if err := store.TouchDeviceHeartbeat(r.Context(), device, clock.Now().UTC()); err != nil {
				slog.ErrorContext(r.Context(), "record device heartbeat failed", "device_id", device.ID, "error", err)
				httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to record device heartbeat", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceContextKey{}, device)))
		})
	}
}

func DeviceRateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	return httprate.Limit(limit, window, httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
		device, ok := DeviceFromContext(r.Context())
		if !ok || device.ID == "" {
			return "", errors.New("authenticated device is missing")
		}
		return device.InstitutionID + ":" + device.ID, nil
	}), httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests", nil)
	}), httprate.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
		slog.ErrorContext(r.Context(), "device rate-limit middleware failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to apply request rate limit", nil)
	}))
}

type Handler struct {
	service *scans.Service
	store   DeviceStore
	clock   Clock
}

func NewHandler(service *scans.Service, store DeviceStore, clock Clock) (*Handler, error) {
	if service == nil || store == nil || clock == nil {
		return nil, errors.New("scan service, device store, and clock are required")
	}
	return &Handler{service: service, store: store, clock: clock}, nil
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Get("/device/tasks", h.tasks)
	router.Post("/device/scans", h.scan)
	router.Post("/device/scans/batch", h.batch)
	router.Post("/device/enrolments/{id}/complete", h.completeEnrolment)
	router.Post("/device/status", h.status)
	router.Post("/device/templates/sync", h.syncTemplates)
}

func (h *Handler) syncTemplates(w http.ResponseWriter, r *http.Request) {
	device, ok := DeviceFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "device_auth_required", "A device key is required", nil)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID != "" {
		if _, err := db.ParseUUID(sessionID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "session_id is invalid", nil)
			return
		}
	}
	result, err := h.store.SyncDeviceTemplates(r.Context(), device, sessionID)
	if errors.Is(err, scans.ErrNoActiveSession) {
		httpx.WriteError(w, http.StatusConflict, "no_active_session", "There is no open session at this device venue", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "sync device templates failed", "device_id", device.ID, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to sync device templates", nil)
		return
	}
	result.ServerTime = h.clock.Now().UTC().Format(time.RFC3339Nano)
	httpx.WriteJSON(w, http.StatusOK, result)
}

type statusRequest struct {
	FirmwareVersion   *string `json:"firmware_version"`
	BatteryPercent    *int    `json:"battery_percent"`
	PendingQueueDepth *int    `json:"pending_queue_depth"`
	FreeStorageBytes  *int64  `json:"free_storage_bytes"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	device, ok := DeviceFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "device_auth_required", "A device key is required", nil)
		return
	}
	var request statusRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if request.BatteryPercent != nil && (*request.BatteryPercent < 0 || *request.BatteryPercent > 100) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "battery_percent must be between 0 and 100", nil)
		return
	}
	if request.PendingQueueDepth != nil && *request.PendingQueueDepth < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "pending_queue_depth must not be negative", nil)
		return
	}
	if request.FreeStorageBytes != nil && *request.FreeStorageBytes < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "free_storage_bytes must not be negative", nil)
		return
	}
	if err := h.store.RecordDeviceStatus(r.Context(), device, scans.DeviceStatus{
		FirmwareVersion:   request.FirmwareVersion,
		BatteryPercent:    request.BatteryPercent,
		PendingQueueDepth: request.PendingQueueDepth,
		FreeStorageBytes:  request.FreeStorageBytes,
	}, h.clock.Now().UTC()); err != nil {
		slog.ErrorContext(r.Context(), "record device status failed", "device_id", device.ID, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to record device status", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) tasks(w http.ResponseWriter, r *http.Request) {
	device, ok := DeviceFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "device_auth_required", "A device key is required", nil)
		return
	}
	tasks, err := h.store.LoadTasks(r.Context(), device.InstitutionID, device.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "load device tasks failed", "device_id", device.ID, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to load device tasks", nil)
		return
	}
	tasks.ServerTime = h.clock.Now().UTC().Format(time.RFC3339Nano)
	etag, err := scans.TasksETag(tasks)
	if err != nil {
		slog.ErrorContext(r.Context(), "generate device task ETag failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to generate task response", nil)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if matchesETag(r.Header.Values("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tasks)
}

func matchesETag(headers []string, etag string) bool {
	for _, header := range headers {
		for _, candidate := range strings.Split(header, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || candidate == etag ||
				strings.TrimPrefix(candidate, "W/") == etag {
				return true
			}
		}
	}
	return false
}

type scanRequest struct {
	SlotNumber      *int       `json:"slot_number"`
	Confidence      *float64   `json:"confidence"`
	ClientScannedAt *time.Time `json:"client_scanned_at"`
}

func (request scanRequest) item() (scans.ScanItem, string) {
	if request.SlotNumber == nil || *request.SlotNumber < 1 {
		return scans.ScanItem{}, "invalid_slot"
	}
	if request.Confidence == nil || math.IsNaN(*request.Confidence) ||
		math.IsInf(*request.Confidence, 0) || *request.Confidence < 0 || *request.Confidence > 1 {
		return scans.ScanItem{}, "invalid_confidence"
	}
	return scans.ScanItem{
		SlotNumber: *request.SlotNumber, Confidence: *request.Confidence, ClientScannedAt: request.ClientScannedAt,
	}, ""
}

func (h *Handler) scan(w http.ResponseWriter, r *http.Request) {
	device, ok := DeviceFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "device_auth_required", "A device key is required", nil)
		return
	}
	var request scanRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	item, validationCode := request.item()
	if validationCode != "" {
		httpx.WriteError(w, http.StatusBadRequest, validationCode, "Scan slot or confidence is invalid", nil)
		return
	}
	result, err := h.service.Process(r.Context(), device, item)
	if err != nil {
		h.writeScanError(w, r, device, result, err)
		return
	}
	status := http.StatusCreated
	if result.Duplicate {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, result)
}

func (h *Handler) batch(w http.ResponseWriter, r *http.Request) {
	device, ok := DeviceFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "device_auth_required", "A device key is required", nil)
		return
	}
	var request struct {
		Scans []scanRequest `json:"scans" validate:"required,min=1,max=100"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Batch must contain between 1 and 100 scans", nil)
		return
	}
	results := make([]scans.BatchItemResult, len(request.Scans))
	for index, item := range request.Scans {
		scanItem, validationCode := item.item()
		if validationCode != "" {
			results[index] = scans.BatchItemResult{
				Index: index, Result: scans.ScanResult{ErrorCode: validationCode},
			}
			continue
		}
		result, err := h.service.Process(r.Context(), device, scanItem)
		if err != nil {
			h.logScanError(r, device, result, err)
		}
		results[index] = scans.BatchItemResult{Index: index, Result: result}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *Handler) completeEnrolment(w http.ResponseWriter, r *http.Request) {
	device, ok := DeviceFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "device_auth_required", "A device key is required", nil)
		return
	}
	taskID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(taskID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Enrolment task ID is invalid", nil)
		return
	}
	var request struct {
		Status   string `json:"status" validate:"required,oneof=done failed"`
		Template string `json:"template"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Status must be done or failed", nil)
		return
	}
	var template []byte
	if request.Template != "" {
		decoded, err := base64.StdEncoding.DecodeString(request.Template)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "template must be base64-encoded", nil)
			return
		}
		template = decoded
	}
	err := h.store.CompleteEnrolment(r.Context(), device.InstitutionID, device.ID, taskID, request.Status, template, h.clock.Now().UTC())
	if errors.Is(err, scans.ErrEnrolmentTaskNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "enrolment_not_found", "Pending enrolment was not found", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "complete enrolment task failed", "device_id", device.ID, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to complete enrolment", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeScanError(w http.ResponseWriter, r *http.Request, device scans.Device, result scans.ScanResult, err error) {
	h.logScanError(r, device, result, err)
	switch {
	case errors.Is(err, scans.ErrNoActiveSession):
		httpx.WriteError(w, http.StatusConflict, "no_active_session", "There is no open session at this device venue", nil)
	case errors.Is(err, scans.ErrInvalidSlot):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_slot", "Device slot was not found", nil)
	case errors.Is(err, scans.ErrInvalidConfidence):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_confidence", "Confidence must be between 0 and 1", nil)
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "scan_failed", "Unable to record scan", nil)
	}
}

func (h *Handler) logScanError(r *http.Request, device scans.Device, result scans.ScanResult, err error) {
	if errors.Is(err, scans.ErrNoActiveSession) || errors.Is(err, scans.ErrInvalidSlot) ||
		errors.Is(err, scans.ErrInvalidConfidence) {
		return
	}
	slog.ErrorContext(r.Context(), "device scan failed", "device_id", device.ID,
		"error_code", result.ErrorCode, "error", err)
}
