package auth_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/notify"
	"attendance-api/internal/testutil"
)

type memoryClaimRepository struct {
	mu            sync.Mutex
	record        auth.StudentRecord
	verifications map[string]verification
	requests      int
}

type verification struct {
	hash      []byte
	expiresAt time.Time
	used      bool
}

func newClaimRepository() *memoryClaimRepository {
	return &memoryClaimRepository{
		record: auth.StudentRecord{
			ID: "record-1", InstitutionID: "institution-1", DepartmentID: "10000000-0000-4000-8000-000000000001",
			MatricNo: "MAT001", Email: "record@example.test", DisplayName: "Student", Level: 200,
		},
		verifications: make(map[string]verification),
	}
}

func (r *memoryClaimRepository) CreateScopedSignupRequest(_ context.Context, institutionID, matric, email, departmentID string) error {
	if institutionID == "" || matric == "" || email == "" || departmentID == "" {
		return errors.New("incomplete scoped request")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests++
	return nil
}

func (r *memoryClaimRepository) FindStudentByMatric(_ context.Context, institutionID, matric string) (auth.StudentRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if institutionID != r.record.InstitutionID || matric != r.record.MatricNo {
		return auth.StudentRecord{}, false, nil
	}
	return r.record, true, nil
}

func (r *memoryClaimRepository) CreateSignupRequest(_ context.Context, institutionID, matric, email string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if institutionID == "" || matric == "" || email == "" {
		return errors.New("incomplete request")
	}
	r.requests++
	return nil
}

func (r *memoryClaimRepository) CreateVerification(_ context.Context, institutionID, recordID string, hash []byte, expiresAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if institutionID != r.record.InstitutionID {
		return errors.New("tenant mismatch")
	}
	r.verifications[recordID] = verification{hash: append([]byte(nil), hash...), expiresAt: expiresAt}
	return nil
}

func (r *memoryClaimRepository) ClaimStudent(_ context.Context, institutionID, recordID string, hash []byte, now time.Time, passwordHash, displayName string) (auth.ClaimedStudent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if institutionID != r.record.InstitutionID {
		return auth.ClaimedStudent{}, auth.ErrInvalidVerification
	}
	verification, ok := r.verifications[recordID]
	if r.record.Claimed {
		return auth.ClaimedStudent{}, auth.ErrStudentAlreadyClaimed
	}
	if !ok || verification.used || !now.Before(verification.expiresAt) || !bytes.Equal(hash, verification.hash) {
		return auth.ClaimedStudent{}, auth.ErrInvalidVerification
	}
	if passwordHash == "" || displayName != r.record.DisplayName {
		return auth.ClaimedStudent{}, errors.New("invalid account details")
	}
	verification.used = true
	r.verifications[recordID] = verification
	r.record.Claimed = true
	return auth.ClaimedStudent{
		UserID: "user-1", StudentID: r.record.ID, InstitutionID: r.record.InstitutionID,
		DepartmentID: r.record.DepartmentID, Level: r.record.Level,
	}, nil
}

func TestStudentClaimVerificationLocksRecordDepartmentAndLevel(t *testing.T) {
	t.Parallel()
	repository := newClaimRepository()
	mailer := &testutil.MemoryMailer{}
	clock := testutil.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	service, err := auth.NewClaimService(repository, mailer, clock, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	// rand.Int reads three bytes for this bound; this produces code 000007.
	if err := service.SetRandomSource(bytes.NewReader([]byte{0, 0, 0, 7})); err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(context.Background(), "institution-1", "MAT001", "RECORD@example.test")
	if err != nil || result.ApprovalRequested {
		t.Fatalf("Start() = %+v, %v", result, err)
	}
	messages := mailer.Sent()
	if len(messages) != 1 || messages[0].To != "record@example.test" {
		t.Fatalf("verification was not sent to record email: %+v", messages)
	}
	claimed, err := service.Verify(context.Background(), "institution-1", "MAT001", "000007", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.DepartmentID != "10000000-0000-4000-8000-000000000001" || claimed.Level != 200 {
		t.Fatalf("claim changed locked student attributes: %+v", claimed)
	}
	if _, err := service.Verify(context.Background(), "institution-1", "MAT001", "000007", "correct horse battery staple"); !errors.Is(err, auth.ErrStudentAlreadyClaimed) {
		t.Fatalf("second claim error = %v", err)
	}
}

func TestUnknownMatricCreatesApprovalRequest(t *testing.T) {
	t.Parallel()
	repository := newClaimRepository()
	mailer := &testutil.MemoryMailer{}
	service, err := auth.NewClaimService(repository, mailer, testutil.NewFakeClock(time.Unix(1_700_000_000, 0)),
		[]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(context.Background(), "institution-1", "UNKNOWN", "unknown@example.test")
	if err != nil || !result.ApprovalRequested {
		t.Fatalf("Start() = %+v, %v", result, err)
	}
	if repository.requests != 1 || len(mailer.Sent()) != 0 {
		t.Fatal("unknown matric did not create a request without sending verification")
	}
}

func TestClaimEmailMismatchCreatesRequestAndExpiredCodeFails(t *testing.T) {
	t.Parallel()
	repository := newClaimRepository()
	mailer := &testutil.MemoryMailer{}
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	service, err := auth.NewClaimService(repository, mailer, clock, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetRandomSource(bytes.NewReader([]byte{0, 0, 0, 9})); err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(context.Background(), "institution-1", "MAT001", "wrong@example.test")
	if !errors.Is(err, auth.ErrClaimEmailMismatch) || result.ApprovalRequested || len(mailer.Sent()) != 0 {
		t.Fatalf("email mismatch result = %+v err=%v", result, err)
	}
	if _, err := service.Start(context.Background(), "institution-1", "MAT001", "record@example.test"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(auth.ClaimCodeLifetime)
	if _, err := service.Verify(context.Background(), "institution-1", "MAT001", "000009", "correct horse battery staple"); !errors.Is(err, auth.ErrInvalidVerification) {
		t.Fatalf("expired code error = %v", err)
	}
}

var _ notify.Mailer = (*testutil.MemoryMailer)(nil)
