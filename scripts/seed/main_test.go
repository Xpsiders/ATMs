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
	for _, credential := range demoCredentials {
		if credential.email == "" || credential.password == "" || !containsTestDomain(credential.email) {
			t.Fatalf("credential is not a development-only account: %+v", credential)
		}
		if _, exists := emails[credential.email]; exists {
			t.Fatalf("duplicate demo email %q", credential.email)
		}
		emails[credential.email] = struct{}{}
	}
}

func containsTestDomain(email string) bool {
	return len(email) >= len("@attendance.test") &&
		email[len(email)-len("@attendance.test"):] == "@attendance.test"
}
