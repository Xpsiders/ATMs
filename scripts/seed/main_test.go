package main

import "testing"

func TestDemoPlanHasRequiredCrossDepartmentCourseCoverage(t *testing.T) {
	t.Parallel()
	plan := demoPlan()
	if err := plan.validate(); err != nil {
		t.Fatal(err)
	}
	if len(plan.departments) != 2 || plan.records != 4 {
		t.Fatalf("demo plan has %d departments and %d records", len(plan.departments), plan.records)
	}
	if plan.offerings[0] != 100 || plan.offerings[1] != 200 {
		t.Fatalf("demo offerings do not cover both levels: %v", plan.offerings)
	}
}

func TestDemoTeachingAndRegistrationFixturesAreConnected(t *testing.T) {
	t.Parallel()
	if len(demoTeachingAssignments) != 2 {
		t.Fatalf("teaching assignments=%d, want one per demo offering", len(demoTeachingAssignments))
	}
	lecturers := map[string]bool{}
	offerings := map[string]bool{}
	venues := map[string]bool{}
	for _, assignment := range demoTeachingAssignments {
		if assignment.lecturerID == "" || assignment.offeringID == "" || assignment.venueID == "" ||
			assignment.timetableID == "" || assignment.weekday < 0 || assignment.weekday > 6 ||
			assignment.startsAt == "" || assignment.endsAt == "" {
			t.Fatalf("incomplete teaching assignment: %+v", assignment)
		}
		lecturers[assignment.lecturerID] = true
		offerings[assignment.offeringID] = true
		venues[assignment.venueID] = true
	}
	if !lecturers[lecturerAID] || !lecturers[lecturerBID] ||
		!offerings[offering100ID] || !offerings[offering200ID] ||
		!venues[venueAID] || !venues[venueBID] {
		t.Fatalf("demo teaching assignments do not connect both lecturers, offerings, and venues: %+v", demoTeachingAssignments)
	}
	if len(demoApprovedRegistrations) != 4 {
		t.Fatalf("approved student registrations=%d, want four", len(demoApprovedRegistrations))
	}
	registered := map[string]bool{}
	for _, registration := range demoApprovedRegistrations {
		if registration.offeringID == "" || registration.studentID == "" {
			t.Fatalf("incomplete approved registration: %+v", registration)
		}
		registered[registration.offeringID+":"+registration.studentID] = true
	}
	for _, expected := range []demoRegistration{
		{offering100ID, student100AID}, {offering100ID, student100BID},
		{offering200ID, student200AID}, {offering200ID, student200BID},
	} {
		if !registered[expected.offeringID+":"+expected.studentID] {
			t.Errorf("missing approved registration %+v", expected)
		}
	}
}

func TestDemoPlanRejectsIncompleteDataset(t *testing.T) {
	t.Parallel()
	plan := demoPlan()
	plan.departments = plan.departments[:1]
	if err := plan.validate(); err == nil {
		t.Fatal("incomplete demo dataset passed validation")
	}
	plan = demoPlan()
	plan.offerings[1] = plan.offerings[0]
	if err := plan.validate(); err == nil {
		t.Fatal("duplicate-level demo offerings passed validation")
	}
}

func TestDemoCredentialsUseUniqueNonProductionAccounts(t *testing.T) {
	t.Parallel()
	emails := make(map[string]struct{}, len(demoCredentials))
	roles := make(map[string]bool)
	for _, credential := range demoCredentials {
		if credential.email == "" || credential.password == "" || !containsTestDomain(credential.email) {
			t.Fatalf("credential is not a development-only account: %+v", credential)
		}
		if _, exists := emails[credential.email]; exists {
			t.Fatalf("duplicate demo email %q", credential.email)
		}
		emails[credential.email] = struct{}{}
		roles[credential.role] = true
		switch credential.scopeType {
		case "platform", "institution":
			if credential.facultyID != "" || credential.deptID != "" {
				t.Fatalf("unexpected resource scope for %s: %+v", credential.role, credential)
			}
		case "faculty":
			if credential.facultyID == "" || credential.deptID != "" {
				t.Fatalf("invalid faculty scope: %+v", credential)
			}
		case "department":
			if credential.facultyID != "" || credential.deptID == "" {
				t.Fatalf("invalid department scope: %+v", credential)
			}
		default:
			t.Fatalf("invalid scope type: %+v", credential)
		}
	}
	for _, role := range []string{
		"platform_admin", "institution_admin", "faculty_admin", "department_admin", "lecturer", "student",
	} {
		if !roles[role] {
			t.Errorf("missing demo account for role %q", role)
		}
	}
}

func containsTestDomain(email string) bool {
	return len(email) >= len("@attendance.test") &&
		email[len(email)-len("@attendance.test"):] == "@attendance.test"
}
