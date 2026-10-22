package catalogue_test

import (
	"context"
	"testing"

	"attendance-api/internal/catalogue"
)

type workflowFakeStore struct {
	fakeStore
	input  catalogue.OfferingInput
	result map[string]any
}

func (f *workflowFakeStore) CreateLecturerOffering(_ context.Context, _, _ string, input catalogue.OfferingInput) (map[string]any, error) {
	f.input = input
	return f.result, nil
}

func (*workflowFakeStore) ListTeachingRequests(context.Context, string, string, int, int) ([]map[string]any, error) {
	return nil, nil
}

func (*workflowFakeStore) ReviewTeachingRequest(context.Context, string, string, string, string) (map[string]any, error) {
	return nil, nil
}

func TestLecturerOfferingInputValidationAndPendingTeachingRequestResponse(t *testing.T) {
	t.Parallel()
	minimum := 75.0
	store := &workflowFakeStore{result: map[string]any{"status": "pending", "teaching_request_id": "request-id"}}
	service, err := catalogue.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	input := catalogue.OfferingInput{
		CourseID: "course-id", SemesterID: "semester-id", Level: 200,
		RegistrationMode: "approval", RosterPolicy: "registered_only", MinAttendancePct: &minimum,
	}
	result, err := service.CreateLecturerOffering(context.Background(), "institution", "lecturer", input)
	if err != nil || result["status"] != "pending" {
		t.Fatalf("result=%v stored input=%+v request=%+v err=%v", result, store.input, input, err)
	}
	overlaps, err := catalogue.TimetableSlotsOverlap("09:00", "10:00", "09:30", "10:30")
	if err != nil || !overlaps {
		t.Fatalf("overlap check = %v, %v", overlaps, err)
	}
}
