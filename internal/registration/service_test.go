package registration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"attendance-api/internal/registration"
	"attendance-api/internal/testutil"
)

type fakeRegistrationStore struct {
	context       registration.Context
	access        registration.Access
	submitted     string
	student       string
	reviewAllowed bool
	reviewStatus  string
}

func (f *fakeRegistrationStore) RegistrationContext(context.Context, string, string, string) (registration.Context, error) {
	return f.context, nil
}

func (f *fakeRegistrationStore) Submit(_ context.Context, _, _, _, status string, _ time.Time) (string, error) {
	f.submitted = status
	return status, nil
}

func (f *fakeRegistrationStore) OfferingAccess(context.Context, string, string, string) (registration.Access, error) {
	return f.access, nil
}

func (f *fakeRegistrationStore) List(_ context.Context, _, _, _, department string, limit, _ int) ([]registration.Registration, error) {
	f.student = department
	rows := make([]registration.Registration, limit)
	for index := range rows {
		rows[index].DepartmentID = department
	}
	return rows, nil
}

func (f *fakeRegistrationStore) CanReview(context.Context, string, string, string, string) (bool, error) {
	return f.reviewAllowed, nil
}

func (f *fakeRegistrationStore) Review(_ context.Context, _, _, _, status string, _ time.Time) (string, error) {
	f.reviewStatus = status
	return status, nil
}

func newRegistrationService(t *testing.T, store *fakeRegistrationStore) *registration.Service {
	t.Helper()
	service, err := registration.NewService(store, testutil.NewFakeClock(time.Unix(1_800_000_000, 0)))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestRegistrationModesThroughService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mode   registration.Mode
		target bool
		want   string
		err    error
	}{
		{"auto target approved", registration.ModeAuto, true, "approved", nil},
		{"auto non-target pending", registration.ModeAuto, false, "pending", nil},
		{"open approved", registration.ModeOpen, false, "approved", nil},
		{"approval pending", registration.ModeApproval, false, "pending", nil},
		{"closed rejected", registration.ModeClosed, false, "", registration.ErrRegistrationClosed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &fakeRegistrationStore{
				context: registration.Context{Mode: test.mode, Policy: registration.RosterRegisteredOnly, AutoTarget: test.target},
			}
			result, err := newRegistrationService(t, store).Register(context.Background(), "institution", "offering", "student")
			if !errors.Is(err, test.err) || result.Status != test.want || store.submitted != test.want {
				t.Fatalf("registration result=%+v err=%v submitted=%q", result, err, store.submitted)
			}
		})
	}
}

func TestRegistrationListScopesDepartmentAndReviewRequiresAuthorization(t *testing.T) {
	t.Parallel()
	store := &fakeRegistrationStore{access: registration.Access{CanView: true, DepartmentFilter: "dept-b"}, reviewAllowed: true}
	service := newRegistrationService(t, store)
	page, err := service.List(context.Background(), "institution", "offering", "user", "pending", 1, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].DepartmentID != "dept-b" || store.student != "dept-b" || !page.HasMore {
		t.Fatalf("registration listing=%+v scope=%q err=%v", page, store.student, err)
	}
	result, err := service.Review(context.Background(), "institution", "offering", "student", "user", "approved")
	if err != nil || result.Status != "approved" || store.reviewStatus != "approved" {
		t.Fatalf("review result=%+v status=%q err=%v", result, store.reviewStatus, err)
	}
	store.reviewAllowed = false
	if _, err := service.Review(context.Background(), "institution", "offering", "student", "user", "rejected"); !errors.Is(err, registration.ErrAccessDenied) {
		t.Fatalf("unauthorized review error=%v", err)
	}
}
