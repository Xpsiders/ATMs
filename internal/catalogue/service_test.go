package catalogue_test

import (
	"context"
	"errors"
	"testing"

	"attendance-api/internal/catalogue"
)

type fakeStore struct {
	allowed           bool
	manageResults     []bool
	manageCalls       int
	destinationFields map[string]any
	filters           map[string]string
}

func (f *fakeStore) List(_ context.Context, _, _, _ string, _ catalogue.Resource, filters map[string]string) ([]map[string]any, error) {
	f.filters = filters
	return []map[string]any{{"id": "resource"}}, nil
}
func (f *fakeStore) Create(context.Context, string, string, catalogue.Resource, map[string]any) (map[string]any, error) {
	return map[string]any{"id": "resource"}, nil
}
func (f *fakeStore) Update(context.Context, string, string, catalogue.Resource, string, map[string]any) (map[string]any, error) {
	return map[string]any{"id": "resource"}, nil
}
func (f *fakeStore) Delete(context.Context, string, string, catalogue.Resource, string) error {
	return nil
}
func (f *fakeStore) CanManage(_ context.Context, _, _ string, _ catalogue.Resource, _ string, fields map[string]any) (bool, error) {
	f.manageCalls++
	if len(f.manageResults) >= f.manageCalls {
		if fields != nil {
			f.destinationFields = fields
		}
		return f.manageResults[f.manageCalls-1], nil
	}
	return f.allowed, nil
}
func (f *fakeStore) CanReadOffering(context.Context, string, string, string) (bool, error) {
	return f.allowed, nil
}
func (f *fakeStore) SetOfferingLecturers(context.Context, string, string, string, []string) error {
	return nil
}
func (f *fakeStore) SetOfferingAutoTargets(context.Context, string, string, []catalogue.AutoTarget) error {
	return nil
}
func (f *fakeStore) OfferingLecturers(context.Context, string, string) ([]map[string]any, error) {
	return []map[string]any{}, nil
}
func (f *fakeStore) OfferingAutoTargets(context.Context, string, string) ([]catalogue.AutoTarget, error) {
	return []catalogue.AutoTarget{}, nil
}

func TestListPreservesCallerFiltersAndRejectsUnknownResources(t *testing.T) {
	t.Parallel()
	store := &fakeStore{allowed: true}
	service, err := catalogue.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	filters := map[string]string{"q": "circuits", "unused": ""}
	if _, err := service.List(context.Background(), "institution", "user", "", catalogue.Offerings, filters); err != nil {
		t.Fatal(err)
	}
	if filters["unused"] != "" || store.filters["q"] != "circuits" {
		t.Fatalf("caller filters=%v store filters=%v", filters, store.filters)
	}
	if _, err := service.List(context.Background(), "institution", "user", "", catalogue.Resource("unknown"), nil); !errors.Is(err, catalogue.ErrInvalidRequest) {
		t.Fatalf("unknown resource error=%v", err)
	}
}

func TestCreateValidatesFieldsAndChecksScope(t *testing.T) {
	t.Parallel()
	store := &fakeStore{allowed: false}
	service, err := catalogue.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), "institution", "user", catalogue.Courses,
		map[string]any{"department_id": "dept", "code": "ECE301"}); !errors.Is(err, catalogue.ErrInvalidRequest) {
		t.Fatalf("missing title error=%v", err)
	}
	if _, err := service.Create(context.Background(), "institution", "user", catalogue.Courses,
		map[string]any{"department_id": "dept", "code": "ECE301", "title": "Circuits"}); !errors.Is(err, catalogue.ErrForbidden) {
		t.Fatalf("unauthorized create error=%v", err)
	}
}

func TestUpdateRequiresAccessToBothCurrentAndDestinationScope(t *testing.T) {
	t.Parallel()
	store := &fakeStore{manageResults: []bool{true, false}}
	service, err := catalogue.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Update(context.Background(), "inst", "dept-admin", catalogue.Courses,
		"course-id", map[string]any{"department_id": "other-department"})
	if !errors.Is(err, catalogue.ErrForbidden) {
		t.Fatalf("moving a course outside the caller's department returned %v", err)
	}
	if store.manageCalls != 2 || store.destinationFields["department_id"] != "other-department" {
		t.Fatalf("expected source and destination authorization, calls=%d destination=%v",
			store.manageCalls, store.destinationFields)
	}
}

func TestOfferingReadDenialForOutOfScopeUser(t *testing.T) {
	t.Parallel()
	store := &fakeStore{allowed: false}
	service, err := catalogue.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.OfferingLecturers(context.Background(), "inst", "other-department-admin", "offering"); !errors.Is(err, catalogue.ErrForbidden) {
		t.Fatalf("out-of-scope offering lecturer read error = %v", err)
	}
	if _, err := service.OfferingAutoTargets(context.Background(), "inst", "other-department-admin", "offering"); !errors.Is(err, catalogue.ErrForbidden) {
		t.Fatalf("out-of-scope offering auto-target read error = %v", err)
	}
}
