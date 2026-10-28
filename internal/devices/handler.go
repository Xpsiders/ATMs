package devices

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"attendance-api/internal/auth"
	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

// Handler serves administrator device-management routes and the device-side
// pairing route.
type Handler struct {
	service *Service
}

// NewHandler validates dependencies and builds the handler.
func NewHandler(service *Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("device service is required")
	}
	return &Handler{service: service}, nil
}

// RegisterAdminRoutes mounts the scoped administrator routes on an authenticated
// router.
func (h *Handler) RegisterAdminRoutes(router chi.Router) {
	router.Post("/devices", h.provision)
	router.Get("/devices", h.list)
	router.Post("/devices/{id}/pairing-code", h.regenerate)
	router.Post("/devices/{id}/rotate-key", h.rotate)
	router.Delete("/devices/{id}", h.revoke)
	router.Post("/devices/{id}/enrolments", h.enqueueEnrolment)
	router.Get("/venues/{id}/scanner-status", h.venueStatus)
}

// RegisterPairingRoutes mounts the unauthenticated device self-pairing route.
func (h *Handler) RegisterPairingRoutes(router chi.Router) {
	router.Post("/device/pair", h.pair)
}

func (h *Handler) provision(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var request struct {
		VenueID string `json:"venue_id" validate:"required,uuid"`
		Name    string `json:"name" validate:"required"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "A venue ID and device name are required", nil)
		return
	}
	result, err := h.service.Provision(r.Context(), actor, ProvisionInput{VenueID: request.VenueID, Name: request.Name})
	if err != nil {
		h.writeError(w, r, err, "provision device")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	limit, offset, ok := devicePagination(w, r)
	if !ok {
		return
	}
	items, hasMore, err := h.service.List(r.Context(), actor, ListFilter{Limit: limit, Offset: offset})
	if err != nil {
		h.writeError(w, r, err, "list devices")
		return
	}
	if items == nil {
		items = []Device{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "limit": limit, "offset": offset, "has_more": hasMore,
	})
}

func (h *Handler) regenerate(w http.ResponseWriter, r *http.Request) {
	h.issue(w, r, false)
}

func (h *Handler) rotate(w http.ResponseWriter, r *http.Request) {
	h.issue(w, r, true)
}

func (h *Handler) issue(w http.ResponseWriter, r *http.Request, rotate bool) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	deviceID := chi.URLParam(r, "id")
	var code PairingCode
	var err error
	if rotate {
		code, err = h.service.RotateKey(r.Context(), actor, deviceID)
	} else {
		code, err = h.service.RegeneratePairingCode(r.Context(), actor, deviceID)
	}
	if err != nil {
		h.writeError(w, r, err, "issue device pairing code")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, code)
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	if err := h.service.Revoke(r.Context(), actor, chi.URLParam(r, "id")); err != nil {
		h.writeError(w, r, err, "revoke device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) enqueueEnrolment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var request struct {
		StudentRecordID string `json:"student_record_id" validate:"required,uuid"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "A student record ID is required", nil)
		return
	}
	result, err := h.service.EnqueueEnrolment(r.Context(), actor, chi.URLParam(r, "id"), request.StudentRecordID)
	if err != nil {
		h.writeError(w, r, err, "enqueue device enrolment")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
}

func (h *Handler) venueStatus(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	status, err := h.service.VenueStatus(r.Context(), actor, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrVenueStatusDenied):
		httpx.WriteError(w, http.StatusForbidden, "venue_status_denied", "You cannot view scanner status for this venue", nil)
	case errors.Is(err, ErrVenueNotFound):
		httpx.WriteError(w, http.StatusNotFound, "venue_not_found", "Venue was not found in this institution", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "load venue scanner status failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to load venue scanner status", nil)
	default:
		httpx.WriteJSON(w, http.StatusOK, status)
	}
}

func (h *Handler) pair(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PairingCode string `json:"pairing_code" validate:"required"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "A pairing code is required", nil)
		return
	}
	result, err := h.service.Pair(r.Context(), request.PairingCode)
	if errors.Is(err, ErrPairingCodeInvalid) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_pairing_code", "Pairing code is invalid or expired", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "device pairing failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to pair device", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"device_key":     result.DeviceKey,
		"device_id":      result.DeviceID,
		"institution_id": result.InstitutionID,
		"venue_id":       result.VenueID,
	})
}

func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return auth.Identity{}, false
	}
	return principal.Identity, true
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error, action string) {
	switch {
	case errors.Is(err, ErrInvalidDeviceRequest):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Device request fields are invalid", nil)
	case errors.Is(err, ErrDeviceScopeDenied):
		httpx.WriteError(w, http.StatusForbidden, "device_scope_denied", ScannerManagementDeniedMessage, nil)
	case errors.Is(err, ErrDeviceNotFound):
		httpx.WriteError(w, http.StatusNotFound, "device_not_found", "Device not found in this institution", nil)
	case errors.Is(err, ErrStudentNotFound):
		httpx.WriteError(w, http.StatusNotFound, "student_not_found", "Student record not found in this institution", nil)
	default:
		slog.ErrorContext(r.Context(), action+" failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to complete device request", nil)
	}
}

func devicePagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 50, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer", nil)
			return 0, 0, false
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "offset must be an integer", nil)
			return 0, 0, false
		}
	}
	if limit < 1 || limit > 200 || offset < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be 1..200 and offset non-negative", nil)
		return 0, 0, false
	}
	return limit, offset, true
}
