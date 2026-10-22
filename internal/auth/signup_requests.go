package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrSignupRequestScopeDenied = errors.New("signup request is outside administrator scope")
	ErrSignupRequestNotFound    = errors.New("signup request was not found or already reviewed")
	ErrSignupStudentExists      = errors.New("student record already exists for this matric number")
	ErrInvalidSignupReview      = errors.New("signup review request is invalid")
)

type SignupRequest struct {
	ID           string    `json:"id"`
	MatricNo     string    `json:"matric_no"`
	Email        string    `json:"email"`
	DepartmentID string    `json:"department_id"`
	CreatedAt    time.Time `json:"created_at"`
}

type SignupRequestRepository interface {
	ListSignupRequests(context.Context, Identity, int, int) ([]SignupRequest, error)
	ReviewSignupRequest(context.Context, Identity, string, string, string, int, string, string) error
}

type SignupRequestService struct {
	repository SignupRequestRepository
}

func NewSignupRequestService(repository SignupRequestRepository) (*SignupRequestService, error) {
	if repository == nil {
		return nil, errors.New("signup request repository is required")
	}
	return &SignupRequestService{repository: repository}, nil
}

func (s *SignupRequestService) List(ctx context.Context, identity Identity, limit, offset int) ([]SignupRequest, bool, error) {
	if identity.InstitutionID == "" || identity.UserID == "" || limit < 1 || limit > 200 || offset < 0 {
		return nil, false, ErrInvalidSignupReview
	}
	items, err := s.repository.ListSignupRequests(ctx, identity, limit+1, offset)
	if err != nil {
		return nil, false, fmt.Errorf("list signup requests: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

func (s *SignupRequestService) Review(
	ctx context.Context,
	identity Identity,
	requestID, status, departmentID string,
	level int,
	programmeID, displayName string,
) error {
	if identity.InstitutionID == "" || identity.UserID == "" ||
		(status != "approved" && status != "rejected") || strings.TrimSpace(requestID) == "" {
		return ErrInvalidSignupReview
	}
	if status == "approved" {
		if strings.TrimSpace(departmentID) == "" || level <= 0 ||
			strings.TrimSpace(programmeID) == "" || strings.TrimSpace(displayName) == "" {
			return ErrInvalidSignupReview
		}
	}
	if err := s.repository.ReviewSignupRequest(ctx, identity, requestID, status,
		departmentID, level, programmeID, strings.TrimSpace(displayName)); err != nil {
		return err
	}
	return nil
}
