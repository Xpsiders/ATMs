package main

import (
	"testing"
	"time"
)

func TestDemoPlanContainsRichDatasetAndEveryRegistrationMode(t *testing.T) {
	t.Parallel()
	plan := demoPlan()
	if err := plan.validate(); err != nil {
		t.Fatal(err)
	}
	if plan.faculties != 2 || plan.departments != 4 || plan.programmes != 4 ||
		plan.courses < 10 || plan.students != 40 || plan.claimedStudents != 8 {
		t.Fatalf("unexpected demo plan: %+v", plan)
	}
	for _, mode := range []string{"auto", "open", "approval", "closed"} {
		if !plan.registrationModes[mode] {
			t.Errorf("demo offerings do not include registration mode %q", mode)
		}
	}
	for _, level := range []int{100, 200, 300, 400} {
		if !plan.levels[level] {
			t.Errorf("demo students/offering fixtures do not include level %d", level)
		}
	}
}

func TestAcademicPeriodContainsSuppliedToday(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		now  time.Time
	}{
		{"first semester", time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC)},
		{"January boundary", time.Date(2027, time.January, 15, 12, 0, 0, 0, time.UTC)},
		{"second semester", time.Date(2027, time.March, 3, 12, 0, 0, 0, time.UTC)},
		{"August boundary", time.Date(2027, time.August, 31, 12, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, semesterStart, semesterEnd := academicPeriod(tt.now)
			if tt.now.Before(start) || !tt.now.Before(end.AddDate(0, 0, 1)) {
				t.Fatalf("academic session %s..%s does not contain %s", start, end, tt.now)
			}
			if tt.now.Before(semesterStart) || !tt.now.Before(semesterEnd.AddDate(0, 0, 1)) {
				t.Fatalf("semester %s..%s does not contain %s", semesterStart, semesterEnd, tt.now)
			}
		})
	}
}

func TestWeeklyAssignmentsIncludeOpenNowAndLaterToday(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC)
	assignments := weeklyAssignments(now)
	if len(assignments) != len(demoCourses) {
		t.Fatalf("assignments = %d, want %d", len(assignments), len(demoCourses))
	}
	for _, assignment := range assignments[:2] {
		if assignment.weekday != int(now.Weekday()) {
			t.Errorf("today's timetable weekday = %d, want %d", assignment.weekday, now.Weekday())
		}
	}
	openStart, err := time.Parse("15:04", assignments[0].startsAt)
	if err != nil {
		t.Fatal(err)
	}
	openEnd, err := time.Parse("15:04", assignments[0].endsAt)
	if err != nil {
		t.Fatal(err)
	}
	todayTime, _ := time.Parse("15:04", now.Format("15:04"))
	if todayTime.Before(openStart) || !todayTime.Before(openEnd) {
		t.Fatalf("open-now timetable range %s..%s does not contain %s", openStart, openEnd, todayTime)
	}
	laterStart, err := time.Parse("15:04", assignments[1].startsAt)
	if err != nil {
		t.Fatal(err)
	}
	if !laterStart.After(todayTime) {
		t.Fatalf("later class starts at %s, not after current time %s", laterStart, todayTime)
	}
}

func TestDemoAccountsCoverRolesAndLecturerStaffFixtures(t *testing.T) {
	t.Parallel()
	roles := map[string]bool{}
	lecturerCount := 0
	seenEmails := map[string]bool{}
	for _, credential := range makeDemoCredentials() {
		if !containsTestDomain(credential.email) || credential.password == "" {
			t.Fatalf("invalid development-only login fixture: %+v", credential)
		}
		if seenEmails[credential.email] {
			t.Fatalf("duplicate demo login email %q", credential.email)
		}
		seenEmails[credential.email] = true
		switch credential.scopeType {
		case "platform", "institution":
			if credential.facultyID != "" || credential.departmentID != "" {
				t.Errorf("unexpected global scope on credential %+v", credential)
			}
		case "faculty":
			if credential.facultyID == "" || credential.departmentID != "" {
				t.Errorf("invalid faculty scope on credential %+v", credential)
			}
		case "department":
			if credential.facultyID != "" || credential.departmentID == "" {
				t.Errorf("invalid department scope on credential %+v", credential)
			}
		default:
			t.Errorf("invalid role scope type %q", credential.scopeType)
		}
		roles[credential.role] = true
		if credential.role == "lecturer" {
			lecturerCount++
		}
	}
	for _, role := range []string{
		"platform_admin", "institution_admin", "faculty_admin", "department_admin", "lecturer", "student",
	} {
		if !roles[role] {
			t.Errorf("seed logins are missing role %q", role)
		}
	}
	if lecturerCount < 3 || len(lecturers()) < lecturerCount {
		t.Fatalf("lecturer accounts=%d staff fixtures=%d, need at least 3", lecturerCount, len(lecturers()))
	}
}

func TestDemoStudentRecordsHaveClaimedAndUnclaimedPairsAcrossDepartments(t *testing.T) {
	t.Parallel()
	students := demoStudents()
	if len(students) < 30 || len(students) > 50 {
		t.Fatalf("student records = %d, want 30..50", len(students))
	}
	departments := map[string]bool{}
	seenMatric := map[string]bool{}
	seenEmail := map[string]bool{}
	claimed, unclaimed := 0, 0
	for _, student := range students {
		departments[student.departmentID] = true
		if seenMatric[student.matric] || seenEmail[student.email] {
			t.Fatalf("duplicate student fixture: matric=%q email=%q", student.matric, student.email)
		}
		seenMatric[student.matric] = true
		seenEmail[student.email] = true
		if student.claimed {
			claimed++
			if student.userID == "" || student.password == "" {
				t.Errorf("claimed record lacks its login: %+v", student)
			}
		} else {
			unclaimed++
			if student.matric == "" || student.email == "" {
				t.Errorf("unclaimed record lacks its claim pair: %+v", student)
			}
		}
	}
	if len(departments) != 4 || claimed == 0 || unclaimed < 10 {
		t.Fatalf("departments=%d claimed=%d unclaimed=%d", len(departments), claimed, unclaimed)
	}
}

func TestAttendanceFixturesCoverReportOutcomesAndLowAttendance(t *testing.T) {
	t.Parallel()
	if demoStudents()[0].level != 100 || len(demoCourses) < 10 {
		t.Fatal("attendance-history student is not enrolled in the historical general offering")
	}
	fixtures := demoAttendanceFixtures()
	outcomes := map[string]bool{}
	attended, absent := 0, 0
	lowStudentID := demoStudents()[0].id
	for _, session := range fixtures {
		for _, event := range session {
			if event.student.id == lowStudentID {
				if event.webStatus != "" || event.scan {
					attended++
				}
				if event.overrideStatus == "absent" {
					absent++
				}
			}
			switch {
			case event.webStatus == "late":
				outcomes["late"] = true
			case event.webStatus != "" && event.scan:
				outcomes["verified"] = true
			case event.webStatus != "":
				outcomes["suspected_proxy"] = true
			case event.scan:
				outcomes["scan_only"] = true
			case event.overrideStatus == "absent":
				outcomes["absent"] = true
			}
		}
	}
	for _, outcome := range []string{"verified", "suspected_proxy", "scan_only", "absent", "late"} {
		if !outcomes[outcome] {
			t.Errorf("attendance history is missing %s fixture", outcome)
		}
	}
	if attended != 5 || absent != 3 || attended*100/(attended+absent) >= 75 {
		t.Fatalf("low-attendance student has %d attended and %d absent sessions", attended, absent)
	}
}

func containsTestDomain(email string) bool {
	const suffix = "@attendance.test"
	return len(email) > len(suffix) && email[len(email)-len(suffix):] == suffix
}
