package catalogue

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound       = errors.New("catalogue item not found")
	ErrInvalidRequest = errors.New("catalogue request is invalid")
	ErrForbidden      = errors.New("catalogue operation is not permitted")
	// ErrResourceInUse is returned when a resource cannot be deleted because other
	// rows still reference it (for example a course that still has offerings).
	ErrResourceInUse = errors.New("catalogue item is still referenced by other records")
)

type Resource string

const (
	Institutions     Resource = "institutions"
	Faculties        Resource = "faculties"
	Departments      Resource = "departments"
	Programmes       Resource = "programmes"
	AcademicSessions Resource = "academic-sessions"
	Semesters        Resource = "semesters"
	Courses          Resource = "courses"
	Offerings        Resource = "offerings"
)

type Store interface {
	List(context.Context, string, string, string, Resource, map[string]string) ([]map[string]any, error)
	Create(context.Context, string, string, Resource, map[string]any) (map[string]any, error)
	Update(context.Context, string, string, Resource, string, map[string]any) (map[string]any, error)
	Delete(context.Context, string, string, Resource, string) error
	CanManage(context.Context, string, string, Resource, string, map[string]any) (bool, error)
	CanReadOffering(context.Context, string, string, string) (bool, error)
	SetOfferingLecturers(context.Context, string, string, string, []string) error
	SetOfferingAutoTargets(context.Context, string, string, []AutoTarget) error
	OfferingLecturers(context.Context, string, string) ([]map[string]any, error)
	OfferingAutoTargets(context.Context, string, string) ([]AutoTarget, error)
	PublicDepartments(context.Context, string) ([]map[string]any, error)
}

type VenueListingStore interface {
	ListVenues(context.Context, string, string, int, int) ([]map[string]any, int64, error)
}

type VenueManagementStore interface {
	CreateVenue(context.Context, string, string, VenueInput) (map[string]any, error)
	UpdateVenue(context.Context, string, string, string, VenueUpdate) (map[string]any, error)
	DeactivateVenue(context.Context, string, string, string) error
}

type VenueInput struct {
	Name         string  `json:"name" validate:"required,max=200"`
	Latitude     float64 `json:"latitude" validate:"gte=-90,lte=90"`
	Longitude    float64 `json:"longitude" validate:"gte=-180,lte=180"`
	RadiusMeters int     `json:"radius_meters" validate:"required,min=1"`
	DepartmentID string  `json:"department_id,omitempty" validate:"omitempty,uuid"`
}

type VenueUpdate struct {
	Name         *string  `json:"name,omitempty" validate:"omitempty,max=200"`
	Latitude     *float64 `json:"latitude,omitempty" validate:"omitempty,gte=-90,lte=90"`
	Longitude    *float64 `json:"longitude,omitempty" validate:"omitempty,gte=-180,lte=180"`
	RadiusMeters *int     `json:"radius_meters,omitempty" validate:"omitempty,min=1"`
	DepartmentID *string  `json:"department_id,omitempty" validate:"omitempty,uuid"`
	Active       *bool    `json:"active,omitempty"`
}

type VenuePage struct {
	Items  []map[string]any `json:"items"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
	Total  int64            `json:"total"`
}

type AutoTarget struct {
	DepartmentID string `json:"department_id" validate:"required,uuid"`
	Level        int    `json:"level" validate:"required,gte=1,lte=1000"`
}

type Service struct {
	store Store
}

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("catalogue store is required")
	}
	return &Service{store: store}, nil
}

func (s *Service) List(ctx context.Context, institutionID, userID, studentID string, resource Resource, filters map[string]string) ([]map[string]any, error) {
	if !validResource(resource) || institutionID == "" || userID == "" {
		return nil, ErrInvalidRequest
	}
	cleanFilters := make(map[string]string, len(filters))
	for key, value := range filters {
		if strings.TrimSpace(value) == "" {
			continue
		}
		cleanFilters[key] = value
	}
	return s.store.List(ctx, institutionID, userID, studentID, resource, cleanFilters)
}

func (s *Service) ListVenues(ctx context.Context, institutionID, userID string, limit, offset int) (VenuePage, error) {
	if institutionID == "" || userID == "" || limit < 1 || limit > 200 || offset < 0 {
		return VenuePage{}, ErrInvalidRequest
	}
	store, ok := s.store.(VenueListingStore)
	if !ok {
		return VenuePage{}, errors.New("venue listing is not supported by this store")
	}
	items, total, err := store.ListVenues(ctx, institutionID, userID, limit, offset)
	if err != nil {
		return VenuePage{}, err
	}
	if items == nil {
		items = []map[string]any{}
	}
	return VenuePage{Items: items, Limit: limit, Offset: offset, Total: total}, nil
}

func (s *Service) CreateVenue(ctx context.Context, institutionID, userID string, input VenueInput) (map[string]any, error) {
	if institutionID == "" || userID == "" || strings.TrimSpace(input.Name) == "" ||
		input.Latitude < -90 || input.Latitude > 90 || input.Longitude < -180 || input.Longitude > 180 ||
		input.RadiusMeters < 1 {
		return nil, ErrInvalidRequest
	}
	store, ok := s.store.(VenueManagementStore)
	if !ok {
		return nil, errors.New("venue management is not supported by this store")
	}
	input.Name = strings.TrimSpace(input.Name)
	return store.CreateVenue(ctx, institutionID, userID, input)
}

func (s *Service) UpdateVenue(ctx context.Context, institutionID, userID, venueID string, update VenueUpdate) (map[string]any, error) {
	if institutionID == "" || userID == "" || venueID == "" ||
		(update.Name == nil && update.Latitude == nil && update.Longitude == nil &&
			update.RadiusMeters == nil && update.DepartmentID == nil && update.Active == nil) {
		return nil, ErrInvalidRequest
	}
	if update.Name != nil {
		trimmed := strings.TrimSpace(*update.Name)
		if trimmed == "" {
			return nil, ErrInvalidRequest
		}
		update.Name = &trimmed
	}
	if update.Latitude != nil && (*update.Latitude < -90 || *update.Latitude > 90) ||
		update.Longitude != nil && (*update.Longitude < -180 || *update.Longitude > 180) ||
		update.RadiusMeters != nil && *update.RadiusMeters < 1 {
		return nil, ErrInvalidRequest
	}
	if update.DepartmentID != nil && strings.TrimSpace(*update.DepartmentID) == "" {
		return nil, ErrInvalidRequest
	}
	store, ok := s.store.(VenueManagementStore)
	if !ok {
		return nil, errors.New("venue management is not supported by this store")
	}
	return store.UpdateVenue(ctx, institutionID, userID, venueID, update)
}

func (s *Service) DeactivateVenue(ctx context.Context, institutionID, userID, venueID string) error {
	if institutionID == "" || userID == "" || venueID == "" {
		return ErrInvalidRequest
	}
	store, ok := s.store.(VenueManagementStore)
	if !ok {
		return errors.New("venue management is not supported by this store")
	}
	return store.DeactivateVenue(ctx, institutionID, userID, venueID)
}

// PublicDepartments lists an institution's departments (id, name, code) without
// authentication, so the pre-login claim/signup screen can show a department
// name picker instead of asking for a raw UUID. Department names are not
// sensitive; results are scoped to the given institution.
func (s *Service) PublicDepartments(ctx context.Context, institutionID string) ([]map[string]any, error) {
	institutionID = strings.TrimSpace(institutionID)
	if institutionID == "" {
		return nil, ErrInvalidRequest
	}
	return s.store.PublicDepartments(ctx, institutionID)
}

func (s *Service) Create(ctx context.Context, institutionID, userID string, resource Resource, fields map[string]any) (map[string]any, error) {
	if !validResource(resource) || institutionID == "" || userID == "" || len(fields) == 0 {
		return nil, ErrInvalidRequest
	}
	if err := validateFields(resource, fields, false); err != nil {
		return nil, err
	}
	allowed, err := s.store.CanManage(ctx, institutionID, userID, resource, "", fields)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	return s.store.Create(ctx, institutionID, userID, resource, fields)
}

func (s *Service) Update(ctx context.Context, institutionID, userID string, resource Resource, id string, fields map[string]any) (map[string]any, error) {
	if !validResource(resource) || institutionID == "" || userID == "" || id == "" || len(fields) == 0 {
		return nil, ErrInvalidRequest
	}
	if err := validateFields(resource, fields, true); err != nil {
		return nil, err
	}
	allowed, err := s.store.CanManage(ctx, institutionID, userID, resource, id, nil)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	if hasScopeChange(resource, fields) {
		allowed, err = s.store.CanManage(ctx, institutionID, userID, resource, "", fields)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrForbidden
		}
	}
	return s.store.Update(ctx, institutionID, userID, resource, id, fields)
}

func (s *Service) Delete(ctx context.Context, institutionID, userID string, resource Resource, id string) error {
	if !validResource(resource) || institutionID == "" || userID == "" || id == "" {
		return ErrInvalidRequest
	}
	allowed, err := s.store.CanManage(ctx, institutionID, userID, resource, id, nil)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return s.store.Delete(ctx, institutionID, userID, resource, id)
}

func (s *Service) SetOfferingLecturers(ctx context.Context, institutionID, userID, offeringID string, lecturerIDs []string) error {
	if institutionID == "" || userID == "" || offeringID == "" {
		return ErrInvalidRequest
	}
	allowed, err := s.store.CanManage(ctx, institutionID, userID, Offerings, offeringID, nil)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return s.store.SetOfferingLecturers(ctx, institutionID, offeringID, userID, lecturerIDs)
}

func (s *Service) SetOfferingAutoTargets(ctx context.Context, institutionID, userID, offeringID string, targets []AutoTarget) error {
	if institutionID == "" || userID == "" || offeringID == "" {
		return ErrInvalidRequest
	}
	allowed, err := s.store.CanManage(ctx, institutionID, userID, Offerings, offeringID, nil)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	seen := map[string]struct{}{}
	for _, target := range targets {
		key := fmt.Sprintf("%s:%d", target.DepartmentID, target.Level)
		if target.DepartmentID == "" || target.Level < 1 || target.Level > 1000 {
			return ErrInvalidRequest
		}
		if _, ok := seen[key]; ok {
			return ErrInvalidRequest
		}
		seen[key] = struct{}{}
	}
	return s.store.SetOfferingAutoTargets(ctx, institutionID, offeringID, targets)
}

func (s *Service) OfferingLecturers(ctx context.Context, institutionID, userID, offeringID string) ([]map[string]any, error) {
	if institutionID == "" || userID == "" || offeringID == "" {
		return nil, ErrInvalidRequest
	}
	allowed, err := s.store.CanReadOffering(ctx, institutionID, userID, offeringID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	return s.store.OfferingLecturers(ctx, institutionID, offeringID)
}

func (s *Service) OfferingAutoTargets(ctx context.Context, institutionID, userID, offeringID string) ([]AutoTarget, error) {
	if institutionID == "" || userID == "" || offeringID == "" {
		return nil, ErrInvalidRequest
	}
	allowed, err := s.store.CanReadOffering(ctx, institutionID, userID, offeringID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	return s.store.OfferingAutoTargets(ctx, institutionID, offeringID)
}

var fieldsByResource = map[Resource]map[string]bool{
	Institutions:     {"name": true},
	Faculties:        {"name": true},
	Departments:      {"faculty_id": true, "name": true, "code": true},
	Programmes:       {"department_id": true, "name": true, "code": true},
	AcademicSessions: {"name": true, "starts_on": true, "ends_on": true, "current": true},
	Semesters:        {"academic_session_id": true, "name": true, "starts_on": true, "ends_on": true},
	Courses:          {"department_id": true, "code": true, "title": true, "kind": true, "description": true, "credits": true},
	Offerings:        {"course_id": true, "semester_id": true, "coordinator_user_id": true, "level": true, "registration_mode": true, "roster_policy": true, "min_attendance_pct": true, "late_counts_as_attended": true},
}

var requiredOnCreate = map[Resource][]string{
	Institutions:     {"name"},
	Faculties:        {"name"},
	Departments:      {"faculty_id", "name", "code"},
	Programmes:       {"department_id", "name", "code"},
	AcademicSessions: {"name", "starts_on", "ends_on"},
	Semesters:        {"academic_session_id", "name", "starts_on", "ends_on"},
	Courses:          {"department_id", "code", "title"},
	Offerings:        {"course_id", "semester_id", "level", "registration_mode"},
}

func validResource(resource Resource) bool {
	_, ok := fieldsByResource[resource]
	return ok
}

func hasScopeChange(resource Resource, fields map[string]any) bool {
	switch resource {
	case Departments:
		_, ok := fields["faculty_id"]
		return ok
	case Programmes, Courses:
		_, ok := fields["department_id"]
		return ok
	case Offerings:
		_, ok := fields["course_id"]
		return ok
	default:
		return false
	}
}

func validateFields(resource Resource, fields map[string]any, partial bool) error {
	allowed, ok := fieldsByResource[resource]
	if !ok {
		return ErrInvalidRequest
	}
	for key, value := range fields {
		if !allowed[key] || value == nil {
			return ErrInvalidRequest
		}
		if text, isText := value.(string); isText && strings.TrimSpace(text) == "" {
			return ErrInvalidRequest
		}
	}
	if !partial {
		for _, key := range requiredOnCreate[resource] {
			if _, ok := fields[key]; !ok {
				return ErrInvalidRequest
			}
		}
	}
	if value, ok := fields["registration_mode"]; ok {
		mode, valid := value.(string)
		if !valid || (mode != "auto" && mode != "open" && mode != "approval" && mode != "closed") {
			return ErrInvalidRequest
		}
	}
	if value, ok := fields["roster_policy"]; ok {
		policy, valid := value.(string)
		if !valid || (policy != "registered_only" && policy != "open") {
			return ErrInvalidRequest
		}
	}
	if value, ok := fields["kind"]; ok {
		kind, valid := value.(string)
		if !valid || (kind != "departmental" && kind != "general") {
			return ErrInvalidRequest
		}
	}
	return nil
}
