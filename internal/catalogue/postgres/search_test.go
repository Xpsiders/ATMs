package postgres

import (
	"encoding/json"
	"testing"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
)

func TestMapSearchOfferingIncludesRegistrationAndCourseMetadata(t *testing.T) {
	t.Parallel()
	offeringID, err := db.ParseUUID("10000000-0000-4000-8000-000000000010")
	if err != nil {
		t.Fatal(err)
	}
	courseID, err := db.ParseUUID("10000000-0000-4000-8000-000000000011")
	if err != nil {
		t.Fatal(err)
	}
	departmentID, err := db.ParseUUID("10000000-0000-4000-8000-000000000003")
	if err != nil {
		t.Fatal(err)
	}
	semesterID, err := db.ParseUUID("10000000-0000-4000-8000-000000000009")
	if err != nil {
		t.Fatal(err)
	}
	item := mapSearchOffering(dbgen.SearchOfferingsFullTextRow{
		OfferingID: offeringID, CourseID: courseID, Code: "ECE301", Title: "Circuits",
		Kind: "departmental", DepartmentID: departmentID, DepartmentName: "Electrical Engineering",
		SemesterID: semesterID, Semester: "First Semester", Rank: 0.8,
	}, offeringSearchMetadata{
		mode: "approval", level: 300, lecturers: []string{"Dr Ada"}, status: "pending",
	})
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{
		"offering_id": "10000000-0000-4000-8000-000000000010",
		"course_id":   "10000000-0000-4000-8000-000000000011",
		"course_code": "ECE301", "course_title": "Circuits",
		"department_id":   "10000000-0000-4000-8000-000000000003",
		"department_name": "Electrical Engineering", "level": float64(300),
		"kind": "departmental", "registration_mode": "approval",
		"registration_status": "pending", "is_registered": false, "is_pending": true,
	}
	for key, want := range expected {
		if got[key] != want {
			t.Errorf("%s=%v, want %v", key, got[key], want)
		}
	}
	if len(got["lecturers"].([]any)) != 1 || got["lecturers"].([]any)[0] != "Dr Ada" {
		t.Errorf("lecturers=%v", got["lecturers"])
	}
}
