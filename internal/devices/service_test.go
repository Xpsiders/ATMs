package devices

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/rbac"
)

const (
	testInstitution = "8a3e2ab7-5f95-4d80-a64a-fbd1bf17d28b"
	testUser        = "11111111-1111-4111-8111-111111111111"
	testVenue       = "48c5efb3-9651-478d-9d70-bb5be469f62f"
	testDevice      = "2f4e4ed0-490c-4cba-9343-62b0744d6ba7"
	testStudent     = "d4f5b2a6-798e-4b2c-bf03-3941827d1ce8"
	testFaculty     = "22222222-2222-4222-8222-222222222222"
	testDepartment  = "33333333-3333-4333-8333-333333333333"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeRepo struct {
	provisionInput ProvisionInput
	provisionCode  PairingCode
	issued         *PairingCode
	issuedRotate   bool
	revoked        string
	enrolDevice    string
	enrolStudent   string
	pairedHash     []byte
	listReturn     []Device

	err error
}

func (r *fakeRepo) authorize(actor auth.Identity, target string) error {
	role := actorRole(actor)
	scope := map[string]string{
		"platform_admin":    "platform",
		"institution_admin": "institution",
		"faculty_admin":     "faculty",
		"department_admin":  "department",
		"lecturer":          "department",
	}[role]
	if !scannerAdminAllows([]rbac.Assignment{{Role: role, ScopeType: rbac.ScopeType(scope), InstitutionID: actor.InstitutionID}}, actor.InstitutionID) {
		return ErrDeviceScopeDenied
	}
	return nil
}

func (r *fakeRepo) Provision(_ context.Context, actor auth.Identity, input ProvisionInput, code PairingCode, _ time.Time) (Device, error) {
	if err := r.authorize(actor, input.VenueID); err != nil {
		return Device{}, err
	}
	r.provisionInput = input
	r.provisionCode = code
	if r.err != nil {
		return Device{}, r.err
	}
	return Device{ID: testDevice, Name: input.Name, VenueID: input.VenueID, Status: StatusUnpaired}, nil
}

func (r *fakeRepo) List(_ context.Context, actor auth.Identity, filter ListFilter) ([]Device, error) {
	if err := r.authorize(actor, actor.InstitutionID); err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(r.listReturn) > filter.Limit {
		return r.listReturn[:filter.Limit], nil
	}
	return r.listReturn, nil
}

func (r *fakeRepo) VenueScannerSnapshot(_ context.Context, actor auth.Identity, venueID string) (ScannerSnapshot, error) {
	if !venueStatusRole(actor) {
		return ScannerSnapshot{}, ErrVenueStatusDenied
	}
	if r.err != nil {
		return ScannerSnapshot{}, r.err
	}
	lastSeen := time.Unix(1_800_000_000, 0).UTC()
	return ScannerSnapshot{Exists: true, LastSeenAt: &lastSeen}, nil
}

func (r *fakeRepo) IssuePairingCode(_ context.Context, actor auth.Identity, deviceID string, code PairingCode, _ time.Time, rotate bool) error {
	if err := r.authorize(actor, deviceID); err != nil {
		return err
	}
	code.DeviceID = deviceID
	r.issued = &code
	r.issuedRotate = rotate
	return r.err
}

func (r *fakeRepo) Revoke(_ context.Context, actor auth.Identity, deviceID string, _ time.Time) error {
	if err := r.authorize(actor, deviceID); err != nil {
		return err
	}
	r.revoked = deviceID
	return r.err
}

func (r *fakeRepo) EnqueueEnrolment(_ context.Context, actor auth.Identity, deviceID, studentRecordID string, _ time.Time) (Enrolment, error) {
	if err := r.authorize(actor, deviceID); err != nil {
		return Enrolment{}, err
	}
	r.enrolDevice = deviceID
	r.enrolStudent = studentRecordID
	if r.err != nil {
		return Enrolment{}, r.err
	}
	return Enrolment{TaskID: "task-1", SlotNumber: 7, Status: "pending"}, nil
}

func (r *fakeRepo) Pair(_ context.Context, codeHash []byte, _ time.Time) (Paired, error) {
	r.pairedHash = codeHash
	if r.err != nil {
		return Paired{}, r.err
	}
	return Paired{DeviceKey: "device-secret", DeviceID: testDevice, InstitutionID: testInstitution, VenueID: testVenue}, nil
}

var _ Repository = (*fakeRepo)(nil)

func actor() auth.Identity {
	return roleActor("institution_admin")
}

func roleActor(role string) auth.Identity {
	return auth.Identity{InstitutionID: testInstitution, UserID: role + "-" + testUser}
}

func venueStatusRole(actor auth.Identity) bool {
	role := actorRole(actor)
	switch role {
	case "platform_admin", "institution_admin", "faculty_admin", "department_admin", "lecturer":
		return true
	default:
		return false
	}
}

func actorRole(actor auth.Identity) string {
	for _, role := range []string{"platform_admin", "institution_admin", "faculty_admin", "department_admin", "lecturer"} {
		if strings.HasPrefix(actor.UserID, role+"-") {
			return role
		}
	}
	return ""
}

func newService(t *testing.T, repo Repository) *Service {
	t.Helper()
	service, err := NewService(repo, fixedClock{now: time.Unix(1_800_000_000, 0).UTC()}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestScannerAdminAllowsOnlyInstitutionAndPlatformAdminsInTenant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		assignment rbac.Assignment
		want       bool
	}{
		{"institution admin", rbac.Assignment{Role: "institution_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: testInstitution}, true},
		{"platform admin", rbac.Assignment{Role: "platform_admin", ScopeType: rbac.ScopePlatform, InstitutionID: testInstitution}, true},
		{"department admin", rbac.Assignment{Role: "department_admin", ScopeType: rbac.ScopeDepartment, InstitutionID: testInstitution, DepartmentID: testDepartment}, false},
		{"faculty admin", rbac.Assignment{Role: "faculty_admin", ScopeType: rbac.ScopeFaculty, InstitutionID: testInstitution, FacultyID: testFaculty}, false},
		{"lecturer", rbac.Assignment{Role: "lecturer", ScopeType: rbac.ScopeDepartment, InstitutionID: testInstitution, DepartmentID: testDepartment}, false},
		{"wrong institution", rbac.Assignment{Role: "institution_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: "99999999-9999-4999-8999-999999999999"}, false},
		{"wrong platform scope", rbac.Assignment{Role: "platform_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: testInstitution}, false},
	}
	for _, tc := range cases {
		if got := scannerAdminAllows([]rbac.Assignment{tc.assignment}, testInstitution); got != tc.want {
			t.Errorf("%s: scannerAdminAllows = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestVenueStatusClassifiesRecentStaleAndMissingScanners(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0).UTC()
	tests := []struct {
		name     string
		snapshot ScannerSnapshot
		want     string
	}{
		{"online", ScannerSnapshot{Exists: true, LastSeenAt: timePointer(now.Add(-30 * time.Second))}, ScannerOnline},
		{"offline", ScannerSnapshot{Exists: true, LastSeenAt: timePointer(now.Add(-2 * time.Minute))}, ScannerOffline},
		{"never seen", ScannerSnapshot{Exists: true}, ScannerOffline},
		{"no scanner", ScannerSnapshot{}, ScannerNoDevice},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &statusRepo{fakeRepo: &fakeRepo{}, snapshot: test.snapshot}
			service, err := NewService(repo, fixedClock{now: now}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			got, err := service.VenueStatus(context.Background(), actor(), testVenue)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != test.want {
				t.Fatalf("status = %q, want %q", got.Status, test.want)
			}
		})
	}
}

type statusRepo struct {
	*fakeRepo
	snapshot ScannerSnapshot
}

func (r *statusRepo) VenueScannerSnapshot(context.Context, auth.Identity, string) (ScannerSnapshot, error) {
	return r.snapshot, nil
}

func timePointer(value time.Time) *time.Time { return &value }

func TestDeviceAndVenueStatusQueriesAreTenantScopedAndDoNotExposeManagementSecrets(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"device list":          deviceListQuery,
		"scanner status":       venueScannerSnapshotQuery,
		"status authorization": venueScannerStatusAuthorizationQuery,
		"role assignments":     deviceAssignmentsQuery,
	} {
		if !strings.Contains(query, "institution_id=$1") {
			t.Errorf("%s query missing tenant condition: %s", name, query)
		}
	}
	for _, forbidden := range []string{"key_hash", "pairing_code", "pairing_codes", "device_key"} {
		if strings.Contains(venueScannerSnapshotQuery, forbidden) {
			t.Errorf("scanner status query references secret/management data %q", forbidden)
		}
		for _, roleScope := range []string{
			"(role='platform_admin' AND scope_type='platform')",
			"(role='institution_admin' AND scope_type='institution')",
			"(role='faculty_admin' AND scope_type='faculty')",
			"(role='department_admin' AND scope_type='department')",
			"(role='lecturer' AND scope_type='department')",
		} {
			if !strings.Contains(venueScannerStatusAuthorizationQuery, roleScope) {
				t.Errorf("read-only status authorization missing role/scope %q", roleScope)
			}
		}
	}
}

func TestDeriveStatus(t *testing.T) {
	t.Parallel()
	if DeriveStatus(true, false) != StatusActive {
		t.Error("paired device should be active")
	}
	if DeriveStatus(false, false) != StatusUnpaired {
		t.Error("keyless device should be unpaired")
	}
	if DeriveStatus(true, true) != StatusRevoked {
		t.Error("disabled device should be revoked regardless of key")
	}
}

func TestProvisionValidatesInputAndIssuesCode(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	service := newService(t, repo)

	if _, err := service.Provision(context.Background(), actor(), ProvisionInput{VenueID: "not-a-uuid", Name: "Scanner"}); !errors.Is(err, ErrInvalidDeviceRequest) {
		t.Fatalf("invalid venue error = %v", err)
	}
	if _, err := service.Provision(context.Background(), actor(), ProvisionInput{VenueID: testVenue, Name: "  "}); !errors.Is(err, ErrInvalidDeviceRequest) {
		t.Fatalf("empty name error = %v", err)
	}

	result, err := service.Provision(context.Background(), actor(), ProvisionInput{VenueID: testVenue, Name: "Lab Scanner"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pairing.Code == "" || len(repo.provisionCode.hash) != 32 {
		t.Fatalf("pairing code not issued: %+v", result.Pairing)
	}
	if !repo.provisionCode.ExpiresAt.After(time.Unix(1_800_000_000, 0).UTC()) {
		t.Fatal("pairing code expiry not in the future")
	}
	if result.Pairing.DeviceID != testDevice {
		t.Fatalf("pairing device id = %q", result.Pairing.DeviceID)
	}
}

func TestListAppliesPaginationHasMore(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{listReturn: []Device{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	service := newService(t, repo)
	items, hasMore, err := service.List(context.Background(), actor(), ListFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !hasMore {
		t.Fatalf("items = %d, hasMore = %v", len(items), hasMore)
	}
}

func TestRotateAndRegenerateForwardRotateFlag(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	service := newService(t, repo)
	if _, err := service.RegeneratePairingCode(context.Background(), actor(), testDevice); err != nil {
		t.Fatal(err)
	}
	if repo.issued == nil || repo.issuedRotate {
		t.Fatalf("regenerate should not rotate: %+v rotate=%v", repo.issued, repo.issuedRotate)
	}
	if _, err := service.RotateKey(context.Background(), actor(), testDevice); err != nil {
		t.Fatal(err)
	}
	if !repo.issuedRotate {
		t.Fatal("rotate should set the rotate flag")
	}
}

func TestEnqueueEnrolmentValidatesIDs(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	service := newService(t, repo)
	if _, err := service.EnqueueEnrolment(context.Background(), actor(), "bad", testStudent); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("bad device error = %v", err)
	}
	if _, err := service.EnqueueEnrolment(context.Background(), actor(), testDevice, "bad"); !errors.Is(err, ErrInvalidDeviceRequest) {
		t.Fatalf("bad student error = %v", err)
	}
	result, err := service.EnqueueEnrolment(context.Background(), actor(), testDevice, testStudent)
	if err != nil {
		t.Fatal(err)
	}
	if result.SlotNumber != 7 || repo.enrolStudent != testStudent {
		t.Fatalf("enrolment result = %+v, student = %q", result, repo.enrolStudent)
	}
}

func TestPairNormalizesAndRejectsBadLength(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	service := newService(t, repo)
	if _, err := service.Pair(context.Background(), "ABC"); !errors.Is(err, ErrPairingCodeInvalid) {
		t.Fatalf("short code error = %v", err)
	}
	if _, err := service.Pair(context.Background(), "k7qf-2m9x"); err != nil {
		t.Fatal(err)
	}
	if !equalBytes(repo.pairedHash, HashPairingCode("K7QF2M9X")) {
		t.Fatal("pair did not hash the canonical code")
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
