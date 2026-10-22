package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"attendance-api/internal/db"
)

var (
	ErrRoleAssignmentDenied       = errors.New("role assignment is outside the caller's authority")
	ErrRoleAssignmentExists       = errors.New("role assignment already exists")
	ErrInvalidRoleAssignment      = errors.New("role assignment is invalid")
	ErrRoleAssignmentUserNotFound = errors.New("role assignment user was not found")
)

type RoleAssignmentRequest struct {
	UserID       string
	Role         string
	ScopeType    string
	FacultyID    string
	DepartmentID string
}

type RoleAssignmentRepository interface {
	AssignRole(context.Context, Identity, RoleAssignmentRequest) error
}

type RoleAssignmentService struct {
	repository RoleAssignmentRepository
}

func NewRoleAssignmentService(repository RoleAssignmentRepository) (*RoleAssignmentService, error) {
	if repository == nil {
		return nil, errors.New("role assignment repository is required")
	}
	return &RoleAssignmentService{repository: repository}, nil
}

func (s *RoleAssignmentService) Assign(ctx context.Context, actor Identity, request RoleAssignmentRequest) error {
	request.UserID = strings.TrimSpace(request.UserID)
	if actor.InstitutionID == "" || actor.UserID == "" || request.UserID == "" {
		return ErrInvalidRoleAssignment
	}
	if _, err := db.ParseUUID(request.UserID); err != nil {
		return ErrInvalidRoleAssignment
	}
	switch {
	case request.Role == "faculty_admin" && request.ScopeType == "faculty" && request.FacultyID != "" && request.DepartmentID == "":
		if _, err := db.ParseUUID(request.FacultyID); err != nil {
			return ErrInvalidRoleAssignment
		}
	case request.Role == "department_admin" && request.ScopeType == "department" && request.DepartmentID != "" && request.FacultyID == "":
		if _, err := db.ParseUUID(request.DepartmentID); err != nil {
			return ErrInvalidRoleAssignment
		}
	default:
		return ErrInvalidRoleAssignment
	}
	if err := s.repository.AssignRole(ctx, actor, request); err != nil {
		return fmt.Errorf("assign administrator role: %w", err)
	}
	return nil
}
