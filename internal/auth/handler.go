package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

const RefreshCookieName = "attendance_refresh"

type Handler struct {
	service      *LoginService
	cookieSecure bool
	sameSite     http.SameSite
}

func NewHandler(service *LoginService, cookieSecure bool, sameSite http.SameSite) (*Handler, error) {
	if service == nil {
		return nil, errors.New("login service is required")
	}
	return &Handler{service: service, cookieSecure: cookieSecure, sameSite: sameSite}, nil
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.With(httpx.RateLimitByIP(5, time.Minute)).Post("/auth/login", h.login)
	router.With(httpx.RateLimitByIP(10, time.Minute)).Post("/auth/refresh", h.refresh)
	router.With(httpx.RateLimitByIP(20, time.Minute)).Post("/auth/logout", h.logout)
	router.With(UserAuth(h.service.tokens), UserRateLimit(10, time.Minute)).Post("/auth/password/change", h.changePassword)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InstitutionID string `json:"institution_id" validate:"required,uuid"`
		Email         string `json:"email" validate:"required,email"`
		Password      string `json:"password" validate:"required"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Institution, email, or password is invalid", nil)
		return
	}
	pair, err := h.service.Login(r.Context(), request.InstitutionID, request.Email, request.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		slog.WarnContext(r.Context(), "login rejected", "institution_id", request.InstitutionID)
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is invalid", nil)
		return
	}
	if errors.Is(err, ErrEmailNotVerified) {
		httpx.WriteError(w, http.StatusForbidden, "email_not_verified", "Student email has not been verified", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "login failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to log in", nil)
		return
	}
	h.setRefreshCookie(w, pair.RefreshToken)
	pair.RefreshToken = ""
	httpx.WriteJSON(w, http.StatusOK, pair)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(RefreshCookieName)
	if err != nil || cookie.Value == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_refresh_token", "Refresh token is missing or invalid", nil)
		return
	}
	pair, err := h.service.Refresh(r.Context(), cookie.Value)
	if err != nil {
		slog.WarnContext(r.Context(), "refresh token rejected", "error", err)
		h.clearRefreshCookie(w)
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_refresh_token", "Refresh token is missing or invalid", nil)
		return
	}
	h.setRefreshCookie(w, pair.RefreshToken)
	pair.RefreshToken = ""
	httpx.WriteJSON(w, http.StatusOK, pair)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(RefreshCookieName)
	if err == nil && cookie.Value != "" {
		if err := h.service.Logout(r.Context(), cookie.Value); err != nil {
			slog.WarnContext(r.Context(), "logout token revocation failed", "error", err)
			h.clearRefreshCookie(w)
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_refresh_token", "Refresh token is missing or invalid", nil)
			return
		}
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	var request struct {
		CurrentPassword string `json:"current_password" validate:"required"`
		NewPassword     string `json:"new_password" validate:"required,min=12,max=72"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Current and new passwords are required; new password must be 12 to 72 bytes", nil)
		return
	}
	pair, err := h.service.ChangePassword(r.Context(), principal.Identity, request.CurrentPassword, request.NewPassword)
	if errors.Is(err, ErrCurrentPassword) {
		httpx.WriteError(w, http.StatusUnauthorized, "current_password_invalid", "Current password is invalid", nil)
		return
	}
	if errors.Is(err, ErrWeakPassword) {
		httpx.WriteError(w, http.StatusBadRequest, "weak_password", "Password must contain between 12 and 72 bytes", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "password change failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to change password", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, pair)
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: RefreshCookieName, Value: token, Path: "/auth", MaxAge: int(RefreshTokenLifetime.Seconds()),
		HttpOnly: true, Secure: h.cookieSecure, SameSite: h.sameSite,
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: RefreshCookieName, Value: "", Path: "/auth", MaxAge: -1,
		HttpOnly: true, Secure: h.cookieSecure, SameSite: h.sameSite,
	})
}
