package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

type ClaimHandler struct {
	service      *ClaimService
	tokens       *TokenManager
	cookieSecure bool
	sameSite     http.SameSite
}

func NewClaimHandler(service *ClaimService, tokens *TokenManager, cookieSecure bool, sameSite http.SameSite) (*ClaimHandler, error) {
	if service == nil || tokens == nil {
		return nil, errors.New("claim service and token manager are required")
	}
	return &ClaimHandler{service: service, tokens: tokens, cookieSecure: cookieSecure, sameSite: sameSite}, nil
}

func (h *ClaimHandler) RegisterRoutes(router chi.Router) {
	router.With(httpx.RateLimitByIP(5, time.Minute)).Post("/auth/signup-claim", h.start)
	router.With(httpx.RateLimitByIP(5, time.Minute)).Post("/auth/verify-email", h.verify)
}

func (h *ClaimHandler) start(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InstitutionID string `json:"institution_id" validate:"required,uuid"`
		MatricNo      string `json:"matric_no" validate:"required,max=100"`
		Email         string `json:"email" validate:"required,email,max=254"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Institution, matric number, or email is invalid", nil)
		return
	}
	_, err := h.service.Start(r.Context(), request.InstitutionID, request.MatricNo, request.Email)
	switch {
	case errors.Is(err, ErrClaimEmailMismatch):
		httpx.WriteError(w, http.StatusBadRequest, "claim_email_mismatch",
			"Email does not match the student record", nil)
	case errors.Is(err, ErrStudentAlreadyClaimed):
		httpx.WriteError(w, http.StatusConflict, "student_already_claimed", "Student record has already been claimed", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "student claim request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to start student claim", nil)
	default:
		httpx.WriteJSON(w, http.StatusAccepted, map[string]string{
			"status":  "request_received",
			"message": "If a matching record exists, a verification email will be sent; otherwise the request will be reviewed.",
		})
	}
}

func (h *ClaimHandler) verify(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InstitutionID string `json:"institution_id" validate:"required,uuid"`
		MatricNo      string `json:"matric_no" validate:"required,max=100"`
		Code          string `json:"code" validate:"required,len=6,numeric"`
		Password      string `json:"password" validate:"required,min=12,max=72"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Verification fields or password are invalid", nil)
		return
	}
	claimed, err := h.service.Verify(r.Context(), request.InstitutionID, request.MatricNo, request.Code, request.Password)
	if errors.Is(err, ErrInvalidVerification) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_verification", "Verification code is invalid, expired, or already used", nil)
		return
	}
	if errors.Is(err, ErrStudentAlreadyClaimed) {
		httpx.WriteError(w, http.StatusConflict, "student_already_claimed", "Student record has already been claimed", nil)
		return
	}
	if errors.Is(err, ErrWeakPassword) {
		httpx.WriteError(w, http.StatusBadRequest, "weak_password", "Password must contain between 12 and 72 bytes", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "student claim verification failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to verify student account", nil)
		return
	}
	identity := Identity{
		InstitutionID: claimed.InstitutionID, UserID: claimed.UserID,
		StudentID: claimed.StudentID, DepartmentID: claimed.DepartmentID, Level: claimed.Level,
	}
	access, err := h.tokens.IssueAccess(identity)
	if err != nil {
		slog.ErrorContext(r.Context(), "issue access token after claim failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to issue access token", nil)
		return
	}
	refresh, err := h.tokens.IssueRefresh(r.Context(), identity)
	if err != nil {
		slog.ErrorContext(r.Context(), "issue refresh token after claim failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to issue refresh token", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: RefreshCookieName, Value: refresh, Path: "/auth", MaxAge: int(RefreshTokenLifetime.Seconds()),
		HttpOnly: true, Secure: h.cookieSecure, SameSite: h.sameSite,
	})
	httpx.WriteJSON(w, http.StatusCreated, TokenPair{
		AccessToken: access, ExpiresIn: int(AccessTokenLifetime.Seconds()),
	})
}
