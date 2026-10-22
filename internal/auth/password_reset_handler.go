package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

type PasswordResetHandler struct {
	service *PasswordResetService
}

func NewPasswordResetHandler(service *PasswordResetService) (*PasswordResetHandler, error) {
	if service == nil {
		return nil, errors.New("password reset service is required")
	}
	return &PasswordResetHandler{service: service}, nil
}

func (h *PasswordResetHandler) RegisterRoutes(router chi.Router) {
	router.With(httpx.RateLimitByIP(5, time.Minute)).Post("/auth/password/forgot", h.forgot)
	router.With(httpx.RateLimitByIP(5, time.Minute)).Post("/auth/password/reset", h.reset)
}

func (h *PasswordResetHandler) forgot(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InstitutionID string `json:"institution_id" validate:"required,uuid"`
		Email         string `json:"email" validate:"required,email,max=254"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Institution ID or email is invalid", nil)
		return
	}
	if err := h.service.Forgot(r.Context(), request.InstitutionID, request.Email); err != nil {
		slog.ErrorContext(r.Context(), "password reset request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to process password reset request", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{
		"status":  "request_received",
		"message": "If an account exists for those details, a password reset code will be sent.",
	})
}

func (h *PasswordResetHandler) reset(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InstitutionID string `json:"institution_id" validate:"required,uuid"`
		Email         string `json:"email" validate:"required,email,max=254"`
		Code          string `json:"code" validate:"required,len=6,numeric"`
		Password      string `json:"password" validate:"required,min=12,max=72"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Password reset fields are invalid", nil)
		return
	}
	err := h.service.Reset(r.Context(), request.InstitutionID, request.Email, request.Code, request.Password)
	switch {
	case errors.Is(err, ErrInvalidPasswordReset):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_reset_code", "Reset code is invalid, expired, or has exceeded the attempt limit", nil)
	case errors.Is(err, ErrWeakPassword):
		httpx.WriteError(w, http.StatusBadRequest, "weak_password", "Password must contain between 12 and 72 bytes", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "password reset completion failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to reset password", nil)
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "password_reset"})
	}
}
