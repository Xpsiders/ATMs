package registration

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrOfferingNotFound     = errors.New("offering not found")
	ErrAccessDenied         = errors.New("registration access denied")
	ErrRegistrationNotFound = errors.New("registration not found")
	ErrInvalidReview        = errors.New("registration review status is invalid")
)

type Context struct {
	Mode       Mode
	Policy     RosterPolicy
	Registered bool
	AutoTarget bool
}

type Registration struct {
	StudentID    string     `json:"student_id"`
	MatricNo     string     `json:"matric_no"`
	DisplayName  string     `json:"display_name"`
	DepartmentID string     `json:"department_id"`
	Level        int        `json:"level"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	OnRoster     bool       `json:"on_roster"`
}

type Listing struct {
	Items   []Registration `json:"items"`
	Limit   int            `json:"limit"`
	Offset  int            `json:"offset"`
	HasMore bool           `json:"has_more"`
}

type ApprovalRequest struct {
	OfferingID   string    `json:"offering_id"`
	CourseCode   string    `json:"course_code"`
	CourseTitle  string    `json:"course_title"`
	StudentID    string    `json:"student_id"`
	MatricNo     string    `json:"matric_no"`
	DisplayName  string    `json:"display_name"`
	DepartmentID string    `json:"department_id"`
	Level        int       `json:"level"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

type ApprovalPage struct {
	Items   []ApprovalRequest `json:"items"`
	Limit   int               `json:"limit"`
	Offset  int               `json:"offset"`
	HasMore bool              `json:"has_more"`
}

type BulkReviewItem struct {
	StudentID string `json:"student_id"`
	Status    string `json:"status,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type Access struct {
	CanView          bool
	DepartmentFilter string
}

type Store interface {
	RegistrationContext(context.Context, string, string, string) (Context, error)
	Submit(context.Context, string, string, string, string, time.Time) (string, error)
	OfferingAccess(context.Context, string, string, string) (Access, error)
	List(context.Context, string, string, string, string, int, int) ([]Registration, error)
	CanReview(context.Context, string, string, string, string) (bool, error)
	Review(context.Context, string, string, string, string, string, time.Time) (string, error)
}

type ApprovalStore interface {
	PendingApprovals(context.Context, string, string, int, int) ([]ApprovalRequest, error)
}
type Clock interface {
	Now() time.Time
}

type Service struct {
	store Store
	clock Clock
}

func NewService(store Store, clock Clock) (*Service, error) {
	if store == nil || clock == nil {
		return nil, errors.New("registration store and clock are required")
	}
	return &Service{store: store, clock: clock}, nil
}

func (s *Service) Register(ctx context.Context, institutionID, offeringID, studentID string) (Registration, error) {
	context, err := s.store.RegistrationContext(ctx, institutionID, offeringID, studentID)
	if err != nil {
		return Registration{}, err
	}
	decision, err := Evaluate(context.Mode, context.Policy, context.Registered, context.AutoTarget)
	if err != nil {
		return Registration{}, err
	}
	status, err := s.store.Submit(ctx, institutionID, offeringID, studentID, decision.Status, s.clock.Now().UTC())
	if err != nil {
		return Registration{}, fmt.Errorf("submit course registration: %w", err)
	}
	decision, err = Evaluate(context.Mode, context.Policy, status == "approved", context.AutoTarget)
	if err != nil {
		return Registration{}, err
	}
	return Registration{StudentID: studentID, Status: status, OnRoster: decision.OnRoster}, nil
}

func (s *Service) List(ctx context.Context, institutionID, offeringID, userID, status string, limit, offset int) (Listing, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 || offset < 0 {
		return Listing{}, errors.New("limit must be 1..200 and offset non-negative")
	}
	if status != "" && status != "pending" && status != "approved" && status != "rejected" {
		return Listing{}, ErrInvalidReview
	}
	access, err := s.store.OfferingAccess(ctx, institutionID, offeringID, userID)
	if err != nil {
		return Listing{}, err
	}
	if !access.CanView {
		return Listing{}, ErrAccessDenied
	}
	rows, err := s.store.List(ctx, institutionID, offeringID, status, access.DepartmentFilter, limit+1, offset)
	if err != nil {
		return Listing{}, fmt.Errorf("list course registrations: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return Listing{Items: rows, Limit: limit, Offset: offset, HasMore: hasMore}, nil
}

func (s *Service) Review(ctx context.Context, institutionID, offeringID, studentID, userID, status string) (Registration, error) {
	if status != "approved" && status != "rejected" {
		return Registration{}, ErrInvalidReview
	}
	allowed, err := s.store.CanReview(ctx, institutionID, offeringID, studentID, userID)
	if err != nil {
		return Registration{}, err
	}
	if !allowed {
		return Registration{}, ErrAccessDenied
	}
	finalStatus, err := s.store.Review(ctx, institutionID, offeringID, studentID, userID, status, s.clock.Now().UTC())
	if err != nil {
		return Registration{}, err
	}
	return Registration{StudentID: studentID, Status: finalStatus, OnRoster: finalStatus == "approved"}, nil
}

func (s *Service) PendingApprovals(ctx context.Context, institutionID, userID string, limit, offset int) (ApprovalPage, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 || offset < 0 {
		return ApprovalPage{}, errors.New("limit must be 1..200 and offset non-negative")
	}
	store, ok := s.store.(ApprovalStore)
	if !ok {
		return ApprovalPage{}, errors.New("pending approval listing is not supported by this store")
	}
	items, err := store.PendingApprovals(ctx, institutionID, userID, limit+1, offset)
	if err != nil {
		return ApprovalPage{}, fmt.Errorf("list pending registrations: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return ApprovalPage{Items: items, Limit: limit, Offset: offset, HasMore: hasMore}, nil
}

func (s *Service) BulkReview(ctx context.Context, institutionID, offeringID, userID string, studentIDs []string, status string) ([]BulkReviewItem, error) {
	if len(studentIDs) == 0 || len(studentIDs) > 200 || (status != "approved" && status != "rejected") {
		return nil, ErrInvalidReview
	}
	items := make([]BulkReviewItem, 0, len(studentIDs))
	seen := make(map[string]struct{}, len(studentIDs))
	for _, studentID := range studentIDs {
		item := BulkReviewItem{StudentID: studentID}
		if _, exists := seen[studentID]; exists {
			item.ErrorCode = "duplicate_student_id"
			items = append(items, item)
			continue
		}
		seen[studentID] = struct{}{}
		_, err := s.Review(ctx, institutionID, offeringID, studentID, userID, status)
		switch {
		case errors.Is(err, ErrAccessDenied):
			item.ErrorCode = "access_denied"
		case errors.Is(err, ErrRegistrationNotFound):
			item.ErrorCode = "not_pending"
		case err != nil:
			return items, fmt.Errorf("review registration for %q: %w", studentID, err)
		default:
			item.Status = status
		}
		items = append(items, item)
	}
	return items, nil
}
