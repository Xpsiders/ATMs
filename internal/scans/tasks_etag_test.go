package scans

import "testing"

func TestTasksETagIgnoresServerTimeAndChangesWithTask(t *testing.T) {
	t.Parallel()
	tasks := Tasks{ActiveSession: &ActiveSession{ID: "s1", VenueID: "v1"}, ServerTime: "2026-01-01T00:00:00Z"}
	first, err := TasksETag(tasks)
	if err != nil {
		t.Fatal(err)
	}
	tasks.ServerTime = "2026-01-01T00:00:01Z"
	second, err := TasksETag(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("ETag changed when only authoritative server time changed")
	}
	tasks.ActiveSession.ID = "s2"
	third, err := TasksETag(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("ETag did not change with the task")
	}
	slotNumber := 61
	tasks.PendingEnrolment = &Enrolment{
		ID: "enrolment-1", SlotNumber: &slotNumber, StudentRef: "ECE-500-01",
	}
	tasks.EnrolmentQueueRevision = "enrolment-1"
	fourth, err := TasksETag(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if fourth == third {
		t.Fatal("ETag did not change when a pending enrolment was queued")
	}
	tasks.EnrolmentQueueRevision = "enrolment-1,enrolment-2"
	fifth, err := TasksETag(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if fifth == fourth {
		t.Fatal("ETag did not change when a task was added behind the pending enrolment")
	}
}
