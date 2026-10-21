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
	Review(context.Context, string, string, string, string, time.Time) (string, error)
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
	finalStatus, err := s.store.Review(ctx, institutionID, offeringID, studentID, status, s.clock.Now().UTC())
	if err != nil {
		return Registration{}, err
	}
	return Registration{StudentID: studentID, Status: finalStatus, OnRoster: finalStatus == "approved"}, nil
}
