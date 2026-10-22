package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

type RoleAssignmentHandler struct {
	service *RoleAssignmentService
}

func NewRoleAssignmentHandler(service *RoleAssignmentService) (*RoleAssignmentHandler, error) {
	if service == nil {
		return nil, errors.New("role assignment service is required")
	}
	return &RoleAssignmentHandler{service: service}, nil
}

func (h *RoleAssignmentHandler) RegisterRoutes(router chi.Router) {
	router.Post("/role-assignments", h.assign)
}

func (h *RoleAssignmentHandler) assign(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	var request struct {
		UserID       string `json:"user_id" validate:"required,uuid"`
		Role         string `json:"role" validate:"required,oneof=faculty_admin department_admin"`
		ScopeType    string `json:"scope_type" validate:"required,oneof=faculty department"`
		FacultyID    string `json:"faculty_id" validate:"omitempty,uuid"`
		DepartmentID string `json:"department_id" validate:"omitempty,uuid"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Role assignment fields are invalid", nil)
		return
	}
	err := h.service.Assign(r.Context(), principal.Identity, RoleAssignmentRequest{
		UserID: request.UserID, Role: request.Role, ScopeType: request.ScopeType,
		FacultyID: request.FacultyID, DepartmentID: request.DepartmentID,
	})
	switch {
	case errors.Is(err, ErrInvalidRoleAssignment):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Role assignment scope does not match the requested role", nil)
	case errors.Is(err, ErrRoleAssignmentDenied):
		httpx.WriteError(w, http.StatusForbidden, "role_assignment_denied", "You cannot assign this role in the requested scope", nil)
	case errors.Is(err, ErrRoleAssignmentExists):
		httpx.WriteError(w, http.StatusConflict, "role_assignment_exists", "This scoped role is already assigned to the user", nil)
	case errors.Is(err, ErrRoleAssignmentUserNotFound):
		httpx.WriteError(w, http.StatusNotFound, "user_not_found", "Target user does not exist in this institution", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "create role assignment failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to assign administrator role", nil)
	default:
		httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "assigned"})
	}
}
