package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"attendance-api/internal/auth"
	"attendance-api/internal/checkins"
	"attendance-api/internal/httpx"
)

type Handler struct {
	service *checkins.Service
}

func NewHandler(service *checkins.Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("check-in service is required")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	if principal.Identity.StudentID == "" {
		httpx.WriteError(w, http.StatusForbidden, "student_account_required", "Only a student account can check in", nil)
		return
	}
	var request struct {
		SessionID  string   `json:"session_id" validate:"required,uuid"`
		Code       string   `json:"code" validate:"required,len=6,numeric"`
		Latitude   *float64 `json:"lat" validate:"required,gte=-90,lte=90"`
		Longitude  *float64 `json:"lng" validate:"required,gte=-180,lte=180"`
		Accuracy   *float64 `json:"accuracy" validate:"required,gte=0"`
		DeviceHash string   `json:"device_hash"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Check-in fields are invalid", nil)
		return
	}
	input := checkins.Request{
		SessionID: request.SessionID, Code: request.Code, Latitude: *request.Latitude,
		Longitude: *request.Longitude, Accuracy: *request.Accuracy, DeviceHash: request.DeviceHash,
	}
	result, code, err := h.service.Checkin(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.StudentID, input)
	if err != nil {
		slog.ErrorContext(r.Context(), "check-in service failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to record check-in", nil)
		return
	}
	if code != "" {
		status := http.StatusBadRequest
		if code == checkins.ErrAlreadyExists {
			status = http.StatusConflict
			slog.WarnContext(r.Context(), "duplicate check-in ignored",
				"institution_id", principal.Identity.InstitutionID,
				"session_id", request.SessionID,
			)
		}
		httpx.WriteError(w, status, string(code), checkinErrorMessage(code), nil)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
}

func checkinErrorMessage(code checkins.ErrorCode) string {
	switch code {
	case checkins.ErrSessionClosed:
		return "Attendance session is closed"
	case checkins.ErrWrongCode:
		return "Session code is wrong or expired"
	case checkins.ErrNotOnRoster:
		return "Student is not registered for this offering"
	case checkins.ErrOutsideRadius:
		return "Student is outside the allowed check-in radius"
	case checkins.ErrAlreadyExists:
		return "Student has already checked in"
	default:
		return "Check-in request is invalid"
	}
}
