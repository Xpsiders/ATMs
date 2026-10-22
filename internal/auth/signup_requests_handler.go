package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"attendance-api/internal/db"
	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

type SignupRequestHandler struct {
	service *SignupRequestService
}

func NewSignupRequestHandler(service *SignupRequestService) (*SignupRequestHandler, error) {
	if service == nil {
		return nil, errors.New("signup request service is required")
	}
	return &SignupRequestHandler{service: service}, nil
}

func (h *SignupRequestHandler) RegisterRoutes(router chi.Router) {
	router.Get("/signup-requests", h.list)
	router.Post("/signup-requests/{id}/review", h.review)
}

func (h *SignupRequestHandler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	limit, offset, ok := signupPagination(w, r)
	if !ok {
		return
	}
	items, hasMore, err := h.service.List(r.Context(), principal.Identity, limit, offset)
	if errors.Is(err, ErrSignupRequestScopeDenied) {
		httpx.WriteError(w, http.StatusForbidden, "signup_scope_denied", "You cannot review signup requests in this scope", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "list signup requests failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to list signup requests", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "limit": limit, "offset": offset, "has_more": hasMore,
	})
}

func (h *SignupRequestHandler) review(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	requestID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(requestID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Signup request ID is invalid", nil)
		return
	}
	var request struct {
		Status       string `json:"status" validate:"required,oneof=approved rejected"`
		DepartmentID string `json:"department_id" validate:"omitempty,uuid"`
		Level        int    `json:"level" validate:"omitempty,min=1"`
		ProgrammeID  string `json:"programme_id" validate:"omitempty,uuid"`
		DisplayName  string `json:"display_name" validate:"omitempty,max=200"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Signup review fields are invalid", nil)
		return
	}
	err := h.service.Review(r.Context(), principal.Identity, requestID, request.Status,
		request.DepartmentID, request.Level, request.ProgrammeID, request.DisplayName)
	switch {
	case errors.Is(err, ErrInvalidSignupReview):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Approval requires department_id, level, programme_id, and display_name", nil)
	case errors.Is(err, ErrSignupRequestScopeDenied):
		httpx.WriteError(w, http.StatusForbidden, "signup_scope_denied", "Signup request or target department is outside your scope", nil)
	case errors.Is(err, ErrSignupRequestNotFound):
		httpx.WriteError(w, http.StatusNotFound, "signup_request_not_found", "Signup request does not exist or has already been reviewed", nil)
	case errors.Is(err, ErrSignupStudentExists):
		httpx.WriteError(w, http.StatusConflict, "student_record_exists", "A student record already exists for this matric number", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "review signup request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to review signup request", nil)
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": request.Status})
	}
}

func signupPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
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
