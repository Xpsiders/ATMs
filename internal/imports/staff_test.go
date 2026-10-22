package imports

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPreviewStaffReturnsValidRowsAndRowErrors(t *testing.T) {
	csv := "staff_no,email,display_name,department_id\n" +
		"ST-001,lecturer@example.test,Lecturer,10000000-0000-4000-8000-000000000001\n" +
		"ST-002,not-an-email,Missing department,not-a-uuid\n" +
		"st-001,other@example.test,Duplicate staff,10000000-0000-4000-8000-000000000001\n"
	preview, err := PreviewStaff(strings.NewReader(csv), MaxStudentCSVBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 1 || preview.Rows[0].StaffNo != "ST-001" {
		t.Fatalf("valid staff rows = %#v", preview.Rows)
	}
	if len(preview.Errors) != 3 {
		t.Fatalf("row errors = %#v, want invalid email, department ID, and duplicate staff number", preview.Errors)
	}
}

func TestPreviewStaffEnforcesUploadLimit(t *testing.T) {
	_, err := PreviewStaff(strings.NewReader("staff_no,email,display_name,department_id\n"), 1)
	if err == nil || !errors.Is(err, ErrCSVTooLarge) {
		t.Fatalf("oversized CSV error = %v", err)
	}
}

type staffImportFake struct {
	validation []RowError
	committed  []StaffRow
}

func (f *staffImportFake) ValidateStudents(context.Context, string, string, []StudentRow) ([]RowError, error) {
	return nil, nil
}

func (f *staffImportFake) CommitStudents(context.Context, string, string, []StudentRow) error {
	return nil
}

func (f *staffImportFake) ValidateStaff(context.Context, string, string, []StaffRow) ([]RowError, error) {
	return f.validation, nil
}

func (f *staffImportFake) CommitStaff(_ context.Context, _, _ string, rows []StaffRow) error {
	f.committed = append([]StaffRow(nil), rows...)
	return nil
}

func TestStaffCommitDoesNotWriteRowsWithStoreValidationErrors(t *testing.T) {
	store := &staffImportFake{validation: []RowError{{Line: 2, Column: "department_id", Message: "outside scope"}}}
	service, err := NewService(store, MaxStudentCSVBytes)
	if err != nil {
		t.Fatal(err)
	}
	input := "staff_no,email,display_name,department_id\nST-001,lecturer@example.test,Lecturer,10000000-0000-4000-8000-000000000001\n"
	preview, err := service.CommitStaff(context.Background(), "institution", "user", strings.NewReader(input))
	if !errors.Is(err, ErrImportPreviewHasErrors) || len(preview.Errors) != 1 {
		t.Fatalf("commit result = %#v, %v", preview, err)
	}
	if len(store.committed) != 0 {
		t.Fatalf("committed rows despite validation errors: %#v", store.committed)
	}
}
