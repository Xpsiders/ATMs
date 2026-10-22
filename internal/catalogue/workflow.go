package catalogue

import (
	"context"
	"errors"
	"fmt"
)

type OfferingInput struct {
	CourseID             string           `json:"course_id" validate:"required,uuid"`
	SemesterID           string           `json:"semester_id" validate:"required,uuid"`
	Level                int              `json:"level" validate:"required,gte=1,lte=1000"`
	RegistrationMode     string           `json:"registration_mode" validate:"required,oneof=auto open approval closed"`
	RosterPolicy         string           `json:"roster_policy" validate:"required,oneof=registered_only open"`
	MinAttendancePct     *float64         `json:"min_attendance_pct" validate:"required,gte=0,lte=100"`
	LateCountsAsAttended *bool            `json:"late_counts_as_attended"`
	AutoTargets          []AutoTarget     `json:"auto_targets"`
	Timetable            []TimetableInput `json:"timetable"`
}

type OfferingWorkflowStore interface {
	CreateLecturerOffering(context.Context, string, string, OfferingInput) (map[string]any, error)
	ListTeachingRequests(context.Context, string, string, int, int) ([]map[string]any, error)
	ReviewTeachingRequest(context.Context, string, string, string, string) (map[string]any, error)
}

type TeachingRequestPage struct {
	Items   []map[string]any `json:"items"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
	HasMore bool             `json:"has_more"`
}

func (s *Service) CreateLecturerOffering(ctx context.Context, institutionID, userID string, input OfferingInput) (map[string]any, error) {
	if input.CourseID == "" || input.SemesterID == "" || input.Level < 1 || input.Level > 1000 ||
		(input.RegistrationMode != "auto" && input.RegistrationMode != "open" &&
			input.RegistrationMode != "approval" && input.RegistrationMode != "closed") ||
		(input.RosterPolicy != "registered_only" && input.RosterPolicy != "open") ||
		input.MinAttendancePct == nil || *input.MinAttendancePct < 0 || *input.MinAttendancePct > 100 {
		return nil, ErrInvalidRequest
	}
	seen := make(map[string]struct{}, len(input.AutoTargets))
	for _, target := range input.AutoTargets {
		key := fmt.Sprintf("%s:%d", target.DepartmentID, target.Level)
		if target.DepartmentID == "" || target.Level < 1 || target.Level > 1000 {
			return nil, ErrInvalidRequest
		}
		if _, exists := seen[key]; exists {
			return nil, ErrInvalidRequest
		}
		seen[key] = struct{}{}
	}
	for _, slot := range input.Timetable {
		if err := normalizeTimetableInput(&slot); err != nil {
			return nil, err
		}
	}
	store, ok := s.store.(OfferingWorkflowStore)
	if !ok {
		return nil, errors.New("offering workflows are not supported by this store")
	}
	return store.CreateLecturerOffering(ctx, institutionID, userID, input)
}

func (s *Service) ListTeachingRequests(ctx context.Context, institutionID, userID string, limit, offset int) (TeachingRequestPage, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 || offset < 0 {
		return TeachingRequestPage{}, ErrInvalidRequest
	}
	store, ok := s.store.(OfferingWorkflowStore)
	if !ok {
		return TeachingRequestPage{}, errors.New("teaching requests are not supported by this store")
	}
	rows, err := store.ListTeachingRequests(ctx, institutionID, userID, limit+1, offset)
	if err != nil {
		return TeachingRequestPage{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return TeachingRequestPage{Items: rows, Limit: limit, Offset: offset, HasMore: hasMore}, nil
}

func (s *Service) ReviewTeachingRequest(ctx context.Context, institutionID, userID, requestID, status string) (map[string]any, error) {
	if status != "approved" && status != "rejected" {
		return nil, ErrInvalidRequest
	}
	store, ok := s.store.(OfferingWorkflowStore)
	if !ok {
		return nil, errors.New("teaching requests are not supported by this store")
	}
	return store.ReviewTeachingRequest(ctx, institutionID, userID, requestID, status)
}
