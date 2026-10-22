package registration_test

import (
	"context"
	"testing"

	"attendance-api/internal/registration"
)

func TestPendingApprovalQueueAndBulkReview(t *testing.T) {
	t.Parallel()
	store := &fakeRegistrationStore{
		reviewAllowed: true,
		pending: []registration.ApprovalRequest{
			{OfferingID: "offering-a", StudentID: "student-a", Status: "pending"},
			{OfferingID: "offering-b", StudentID: "student-b", Status: "pending"},
			{OfferingID: "offering-c", StudentID: "student-c", Status: "pending"},
		},
	}
	service := newRegistrationService(t, store)
	page, err := service.PendingApprovals(context.Background(), "institution", "lecturer", 2, 1)
	if err != nil || !page.HasMore || len(page.Items) != 2 ||
		store.pendingUser != "lecturer" || store.pendingLimit != 3 || store.pendingOffset != 1 {
		t.Fatalf("pending page=%+v scope=%q limit=%d offset=%d err=%v",
			page, store.pendingUser, store.pendingLimit, store.pendingOffset, err)
	}
	items, err := service.BulkReview(context.Background(), "institution", "offering", "lecturer",
		[]string{"student-a", "student-a", "student-b"}, "approved")
	if err != nil || len(items) != 3 || items[0].Status != "approved" ||
		items[1].ErrorCode != "duplicate_student_id" || items[2].Status != "approved" {
		t.Fatalf("bulk review=%+v err=%v", items, err)
	}
}
