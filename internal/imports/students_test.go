package imports

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

const validHeader = "matric_no,email,display_name,department_id,level\n"
const validDepartmentID = "10000000-0000-4000-8000-000000000003"

func TestPreviewStudentsValidAndRowValidation(t *testing.T) {
	t.Parallel()
	csv := validHeader +
		"MAT001,student@example.test,Student One," + validDepartmentID + ",200\n" +
		"MAT001,second@example.test,Student Two,10000000-0000-4000-8000-000000000004,0\n" +
		",bad@example.test,," + validDepartmentID + ",invalid\n"
	preview, err := PreviewStudents(strings.NewReader(csv), MaxStudentCSVBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 1 || preview.Rows[0].MatricNo != "MAT001" {
		t.Fatalf("valid preview rows = %+v", preview.Rows)
	}
	if len(preview.Errors) < 5 {
		t.Fatalf("row errors = %+v", preview.Errors)
	}
}

func TestPreviewStudentsRejectsMissingColumnsDuplicateHeaderAndOversize(t *testing.T) {
	t.Parallel()
	if _, err := PreviewStudents(strings.NewReader("matric_no,email\n"), MaxStudentCSVBytes); err == nil {
		t.Fatal("missing columns accepted")
	}
	if _, err := PreviewStudents(strings.NewReader(validHeader+"MAT001,a@b.test,One,"+validDepartmentID+",1\n"), 8); err == nil {
		t.Fatal("oversized input accepted")
	}
	if _, err := PreviewStudents(strings.NewReader("matric_no,email,email,display_name,department_id,level\n"), MaxStudentCSVBytes); err == nil {
		t.Fatal("duplicate header accepted")
	}
}

func TestPreviewStudentsMalformedAndShortRows(t *testing.T) {
	t.Parallel()
	csv := validHeader + "MAT001,a@b.test,One," + validDepartmentID + "\n" + `"broken,a@b.test,Two,10000000-0000-4000-8000-000000000004,2`
	preview, err := PreviewStudents(strings.NewReader(csv), MaxStudentCSVBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Errors) != 2 {
		t.Fatalf("malformed rows errors = %+v", preview.Errors)
	}
}

type fakeStudentStore struct {
	mu               sync.Mutex
	calls            int
	rows             []StudentRow
	validationErrors []RowError
}

func (s *fakeStudentStore) ValidateStudents(_ context.Context, institutionID, userID string, _ []StudentRow) ([]RowError, error) {
	if institutionID == "" || userID == "" {
		return nil, errors.New("missing actor identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RowError(nil), s.validationErrors...), nil
}

func (s *fakeStudentStore) CommitStudents(_ context.Context, institutionID, userID string, rows []StudentRow) error {
	if institutionID == "" || userID == "" {
		return errors.New("missing actor identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.rows = append([]StudentRow(nil), rows...)
	return nil
}

func TestDryRunDoesNotCommitAndCommitRequiresCleanPreview(t *testing.T) {
	t.Parallel()
	store := &fakeStudentStore{}
	service, err := NewService(store, MaxStudentCSVBytes)
	if err != nil {
		t.Fatal(err)
	}
	source := validHeader + "MAT001,student@example.test,Student," + validDepartmentID + ",200\n"
	if _, err := service.Preview(strings.NewReader(source)); err != nil || store.calls != 0 {
		t.Fatalf("preview committed data: err=%v calls=%d", err, store.calls)
	}
	if _, err := service.Commit(context.Background(), "institution-1", "admin-1", strings.NewReader(source)); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || len(store.rows) != 1 {
		t.Fatalf("commit calls=%d rows=%+v", store.calls, store.rows)
	}
	invalid := validHeader + "MAT001,bad,Student," + validDepartmentID + ",200\n"
	preview, err := service.Commit(context.Background(), "institution-1", "admin-1", strings.NewReader(invalid))
	if !errors.Is(err, ErrImportPreviewHasErrors) || len(preview.Errors) == 0 || store.calls != 1 {
		t.Fatalf("invalid commit preview=%+v err=%v calls=%d", preview, err, store.calls)
	}
}

func TestPreviewChecksDepartmentAndExistingMatricWithoutCommitting(t *testing.T) {
	t.Parallel()
	store := &fakeStudentStore{validationErrors: []RowError{{
		Line: 2, Column: "department_id", Message: "department is outside your import scope",
	}}}
	service, err := NewService(store, MaxStudentCSVBytes)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewForUser(context.Background(), "institution-1", "dept-admin-1",
		strings.NewReader(validHeader+"MAT001,student@example.test,Student,"+validDepartmentID+",200\n"))
	if err != nil || len(preview.Errors) != 1 || store.calls != 0 {
		t.Fatalf("scoped preview=%+v err=%v commits=%d", preview, err, store.calls)
	}
}

func TestPreviewStudentsRejectsInvalidDepartmentUUIDAndEmptyRows(t *testing.T) {
	t.Parallel()
	invalid, err := PreviewStudents(strings.NewReader(validHeader+"MAT001,student@example.test,Student,not-a-uuid,200\n"), MaxStudentCSVBytes)
	if err != nil || len(invalid.Rows) != 0 || len(invalid.Errors) != 1 || invalid.Errors[0].Column != "department_id" {
		t.Fatalf("invalid department preview=%+v err=%v", invalid, err)
	}
	empty, err := PreviewStudents(strings.NewReader(validHeader), MaxStudentCSVBytes)
	if err != nil || len(empty.Errors) != 1 || empty.Errors[0].Line != 2 {
		t.Fatalf("empty preview=%+v err=%v", empty, err)
	}
}

func TestPreviewStudentsReturnsTypedCSVErrors(t *testing.T) {
	t.Parallel()
	if _, err := PreviewStudents(strings.NewReader("matric_no,email\n"), MaxStudentCSVBytes); !errors.Is(err, ErrInvalidCSV) {
		t.Fatalf("missing column error=%v", err)
	}
	if _, err := PreviewStudents(strings.NewReader(validHeader), 8); !errors.Is(err, ErrCSVTooLarge) {
		t.Fatalf("oversized CSV error=%v", err)
	}
}
