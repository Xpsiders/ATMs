package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

type InvitationHandler struct {
	service      *InvitationService
	tokens       *TokenManager
	cookieSecure bool
	sameSite     http.SameSite
}

func NewInvitationHandler(
	service *InvitationService,
	tokens *TokenManager,
	cookieSecure bool,
	sameSite http.SameSite,
) (*InvitationHandler, error) {
	if service == nil || tokens == nil {
		return nil, errors.New("invitation service and token manager are required")
	}
	return &InvitationHandler{
		service: service, tokens: tokens, cookieSecure: cookieSecure, sameSite: sameSite,
	}, nil
}

func (h *InvitationHandler) RegisterAuthenticatedRoutes(router chi.Router) {
	router.Post("/invitations", h.create)
}

func (h *InvitationHandler) RegisterPublicRoutes(router chi.Router) {
	router.With(httpx.RateLimitByIP(5, time.Minute)).Post("/invitations/accept", h.accept)
}

func (h *InvitationHandler) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	var request struct {
		Email        string `json:"email" validate:"required,email,max=254"`
		Role         string `json:"role" validate:"required,oneof=institution_admin faculty_admin department_admin lecturer"`
		ScopeType    string `json:"scope_type" validate:"required,oneof=institution faculty department"`
		FacultyID    string `json:"faculty_id" validate:"omitempty,uuid"`
		DepartmentID string `json:"department_id" validate:"omitempty,uuid"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Invitation fields are invalid", nil)
		return
	}
	invitation, err := h.service.Create(r.Context(), principal.Identity, InvitationRequest{
		Email: request.Email, Role: request.Role, ScopeType: request.ScopeType,
		FacultyID: request.FacultyID, DepartmentID: request.DepartmentID,
	})
	switch {
	case errors.Is(err, ErrInvalidInvitationRequest):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Invitation fields are invalid", nil)
	case errors.Is(err, ErrInvitationScopeDenied):
		httpx.WriteError(w, http.StatusForbidden, "invitation_scope_denied", "You cannot invite this role into the requested scope", nil)
	case errors.Is(err, ErrInvitationAccountExists):
		httpx.WriteError(w, http.StatusConflict, "email_already_registered", "An account with this email already exists", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "create staff invitation failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to create invitation", nil)
	default:
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
			"status": "invitation_sent", "expires_at": invitation.ExpiresAt,
		})
	}
}

func (h *InvitationHandler) accept(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InstitutionID string `json:"institution_id" validate:"required,uuid"`
		Token         string `json:"token" validate:"required,min=40,max=64"`
		DisplayName   string `json:"display_name" validate:"required,max=200"`
		Password      string `json:"password" validate:"required,min=12,max=72"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Invitation token, display name, or password is invalid", nil)
		return
	}
	identity, err := h.service.Accept(r.Context(), request.InstitutionID, request.Token, request.DisplayName, request.Password)
	switch {
	case errors.Is(err, ErrInvalidInvitation):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_invitation", "Invitation token is invalid, expired, or already used", nil)
		return
	case errors.Is(err, ErrWeakPassword):
		httpx.WriteError(w, http.StatusBadRequest, "weak_password", "Password must contain between 12 and 72 bytes", nil)
		return
	case errors.Is(err, ErrInvitationAccountExists):
		httpx.WriteError(w, http.StatusConflict, "email_already_registered", "An account with this email already exists", nil)
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "accept staff invitation failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to accept invitation", nil)
		return
	}
	access, err := h.tokens.IssueAccess(identity)
	if err != nil {
		slog.ErrorContext(r.Context(), "issue invitation access token failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to sign in", nil)
		return
	}
	refresh, err := h.tokens.IssueRefresh(r.Context(), identity)
	if err != nil {
		slog.ErrorContext(r.Context(), "issue invitation refresh token failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to sign in", nil)
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
