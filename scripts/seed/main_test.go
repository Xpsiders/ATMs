package main

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSeedPlanCoversFrontendDataset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC)
	plan, err := buildSeedPlan(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.validate(); err != nil {
		t.Fatal(err)
	}
	if len(plan.faculties) != 2 || len(plan.departments) != 4 || len(plan.programmes) != 4 ||
		len(plan.courses) != 60 || len(plan.offerings) != 60 || len(plan.venues) != 4 {
		t.Fatalf("unexpected organization/catalogue counts: faculties=%d departments=%d programmes=%d courses=%d offerings=%d venues=%d",
			len(plan.faculties), len(plan.departments), len(plan.programmes),
			len(plan.courses), len(plan.offerings), len(plan.venues))
	}
	if len(plan.students) != 60 {
		t.Fatalf("student records=%d, want 60", len(plan.students))
	}
	claimed, unclaimed := 0, 0
	for _, student := range plan.students {
		if student.userID == "" {
			unclaimed++
		} else {
			claimed++
		}
	}
	if claimed != 40 || unclaimed != 20 {
		t.Fatalf("claimed=%d unclaimed=%d, want 40 and 20", claimed, unclaimed)
	}
}

// The catalogue has to be big enough to exercise search, filters and the
// lecturer picker, and spread over the levels a real department teaches.
func TestSeedCatalogueDepthSpansLevelsAndGeneralCourses(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	general, codes := 0, make(map[string]bool, len(plan.courses))
	for _, course := range plan.courses {
		if codes[course.code] {
			t.Fatalf("duplicate course code %q", course.code)
		}
		codes[course.code] = true
		if course.kind == "general" {
			general++
		}
	}
	if general < 20 {
		t.Errorf("general courses=%d, want at least 20", general)
	}
	levels := make(map[int]int, 5)
	departments := make(map[string]int, len(plan.departments))
	for _, offering := range plan.offerings {
		levels[offering.level]++
		if offering.lecturerID == "" {
			t.Fatalf("offering %s has no lecturer", offering.id)
		}
		course := courseByID(plan.courses, offering.courseID)
		departments[course.departmentID]++
	}
	for _, level := range []int{200, 300, 400, 500} {
		if levels[level] == 0 {
			t.Errorf("no offering at %d level", level)
		}
	}
	if len(departments) != len(plan.departments) {
		t.Errorf("courses cover %d departments, want %d", len(departments), len(plan.departments))
	}
	// Every level with offerings needs students who can register for them.
	studentLevels := make(map[int]int, 5)
	for _, student := range plan.students {
		studentLevels[student.level]++
	}
	for level := range levels {
		if studentLevels[level] == 0 {
			t.Errorf("offerings exist at %d level with no students at that level", level)
		}
	}
	// The extra courses must be shared out, not piled on one lecturer each.
	lecturers := make(map[string]int, 16)
	for _, offering := range plan.offerings {
		lecturers[offering.lecturerID]++
	}
	if len(lecturers) < 8 {
		t.Errorf("offerings are spread over %d lecturers, want at least 8", len(lecturers))
	}
}

func TestSeedUsesHumanReadableUniqueNamesForStudentsAndLecturers(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	studentUsers := make(map[string]string)
	lecturerNames := make(map[string]bool)
	lecturerCount := 0
	for _, user := range plan.users {
		switch user.role {
		case "student":
			studentUsers[user.id] = user.label
		case "lecturer":
			lecturerCount++
			lecturerNames[user.label] = true
			if strings.Contains(strings.ToLower(user.label), "lecturer") ||
				strings.Contains(strings.ToLower(user.label), "engineering") ||
				strings.Contains(strings.ToLower(user.label), "computer science") {
				t.Errorf("lecturer display name contains a role or department label: %q", user.label)
			}
		}
	}
	if len(lecturerNames) != lecturerCount {
		t.Fatalf("unique lecturer display names=%d, want %d (one per lecturer)", len(lecturerNames), lecturerCount)
	}
	if lecturerCount <= len(plan.departments) {
		t.Fatalf("seeded lecturers=%d, want more than one per department for picker options", lecturerCount)
	}

	studentNames := make(map[string]bool, len(plan.students))
	for _, student := range plan.students {
		if student.name == "" || strings.Contains(strings.ToLower(student.name), "student") ||
			strings.Contains(student.name, student.departmentID) {
			t.Errorf("student has a non-human-readable display name %q", student.name)
		}
		if studentNames[student.name] {
			t.Errorf("student display name is not unique: %q", student.name)
		}
		studentNames[student.name] = true
		if student.userID != "" && studentUsers[student.userID] != student.name {
			t.Errorf("claimed student account name %q does not match student record name %q",
				studentUsers[student.userID], student.name)
		}
	}
}

func TestSeedVenuesStartUnlocatedAndSessionsStayAtPrimaryVenue(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	venueByID := make(map[string]seedVenue, len(plan.venues))
	scannerIDs := make(map[string]bool, len(plan.venues))
	for _, venue := range plan.venues {
		venueByID[venue.id] = venue
		if venue.located || venue.lat != 0 || venue.lng != 0 || venue.radius != 0 {
			t.Errorf("seed venue %q starts with a location or radius: %+v", venue.name, venue)
		}
		if venue.device == "" || scannerIDs[venue.device] {
			t.Errorf("venue %q has missing or shared scanner ID %q", venue.name, venue.device)
		}
		scannerIDs[venue.device] = true
	}
	offeringByID := make(map[string]seedOffering, len(plan.offerings))
	for _, offering := range plan.offerings {
		offeringByID[offering.id] = offering
	}
	timetableByID := make(map[string]seedTimetable, len(plan.timetables))
	venuesWithAssignedOffering := make(map[string]bool, len(plan.venues))
	for _, timetable := range plan.timetables {
		timetableByID[timetable.id] = timetable
		if _, ok := offeringByID[timetable.offeringID]; !ok {
			t.Errorf("timetable %s has missing offering %s", timetable.id, timetable.offeringID)
		} else {
			venuesWithAssignedOffering[timetable.venueID] = true
		}
		if _, ok := venueByID[timetable.venueID]; !ok {
			t.Errorf("timetable %s has missing venue %s", timetable.id, timetable.venueID)
		}
	}
	for _, venue := range plan.venues {
		if !venuesWithAssignedOffering[venue.id] {
			t.Errorf("venue %s has no timetable for an assigned offering", venue.id)
		}
	}
	for _, session := range plan.sessions {
		if session.venueID != plan.venues[0].id {
			t.Errorf("seed session %s uses secondary venue %s, want primary %s",
				session.id, session.venueID, plan.venues[0].id)
		}
		if session.located || session.latitude != 0 || session.longitude != 0 || session.radius != 0 {
			t.Errorf("seed session %s has a location or radius: %+v", session.id, session)
		}
		timetable, ok := timetableByID[session.timetable]
		if !ok || timetable.offeringID != session.offeringID || timetable.venueID != session.venueID {
			t.Errorf("session %s is not linked to its matching timetable", session.id)
		}
		offering, ok := offeringByID[session.offeringID]
		if !ok {
			t.Errorf("session %s has missing offering %s", session.id, session.offeringID)
		} else if session.lecturerID != offering.lecturerID {
			t.Errorf("session %s lecturer %s does not match assigned lecturer %s",
				session.id, session.lecturerID, offering.lecturerID)
		}
	}
}

func TestSeedPlanRegistrationModesAndStaffAssignments(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	modes := make(map[string]bool)
	lecturers := make(map[string]bool)
	staffNumbers := make(map[string]bool)
	lecturerIDs := make(map[string]bool)
	for _, offering := range plan.offerings {
		modes[offering.mode] = true
		lecturers[offering.lecturerID] = true
	}
	for _, user := range plan.users {
		if user.role == "lecturer" {
			if staffNumbers[user.staffNo] {
				t.Errorf("duplicate lecturer staff number %q", user.staffNo)
			}
			staffNumbers[user.staffNo] = true
			lecturerIDs[user.id] = true
		}
	}
	// Every offering must be assigned to a real lecturer, but unassigned
	// lecturers are allowed on purpose so the picker has spare options.
	for lecturerID := range lecturers {
		if !lecturerIDs[lecturerID] {
			t.Errorf("offering assigned to non-lecturer user %s", lecturerID)
		}
	}
	for _, mode := range []string{"auto", "open", "approval", "closed"} {
		if !modes[mode] {
			t.Errorf("missing registration mode %q", mode)
		}
	}
	if len(lecturers) < 3 || len(staffNumbers) < 3 {
		t.Fatalf("assigned lecturers=%d staff records=%d, want at least three", len(lecturers), len(staffNumbers))
	}
	for _, registration := range plan.registrations {
		if registration.status != "approved" && registration.status != "pending" {
			t.Errorf("invalid seeded registration status %q", registration.status)
		}
	}
	statuses := make(map[string]map[string]bool)
	for _, registration := range plan.registrations {
		if statuses[registration.offeringID] == nil {
			statuses[registration.offeringID] = make(map[string]bool)
		}
		statuses[registration.offeringID][registration.status] = true
	}
	for index, offering := range plan.offerings {
		switch offering.mode {
		case "auto", "open":
			if !statuses[offering.id]["approved"] {
				t.Errorf("%s offering %s has no approved demo registration", offering.mode, offering.id)
			}
		case "approval":
			if !statuses[offering.id]["pending"] {
				t.Errorf("approval offering %s has no pending demo registration", offering.id)
			}
		case "closed":
			if len(statuses[offering.id]) != 0 {
				t.Errorf("closed offering %d unexpectedly has seeded registrations", index)
			}
		}
	}
}

func TestSeedLecturersAreAssignedOnlyToTheirDepartmentCourses(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	courseDepartments := make(map[string]string, len(plan.courses))
	for _, course := range plan.courses {
		courseDepartments[course.id] = course.departmentID
	}
	lecturerDepartments := make(map[string]string)
	assignedLecturers := make(map[string]bool)
	for _, lecturer := range plan.users {
		if lecturer.role == "lecturer" {
			lecturerDepartments[lecturer.id] = lecturer.deptID
		}
	}
	for _, offering := range plan.offerings {
		assignedLecturers[offering.lecturerID] = true
		if lecturerDepartments[offering.lecturerID] != courseDepartments[offering.courseID] {
			t.Errorf("lecturer %s from department %s is assigned course %s owned by department %s",
				offering.lecturerID, lecturerDepartments[offering.lecturerID],
				offering.courseID, courseDepartments[offering.courseID])
		}
	}
	for lecturerID := range lecturerDepartments {
		if !assignedLecturers[lecturerID] {
			t.Errorf("seed lecturer %s has no offering assignment and cannot start a seeded class", lecturerID)
		}
	}
}

func TestSeedAccountsHaveAllRoleLoginsAndScopedStaffRecords(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	roles := make(map[string]bool)
	emails := make(map[string]bool)
	lecturerStaff := 0
	for _, user := range plan.users {
		roles[user.role] = true
		if emails[user.email] {
			t.Fatalf("duplicate demo login email %q", user.email)
		}
		emails[user.email] = true
		switch user.scopeType {
		case "platform", "institution":
			if user.facultyID != "" || user.deptID != "" {
				t.Errorf("unexpected global scope for %s", user.email)
			}
		case "faculty":
			if user.facultyID == "" || user.deptID != "" {
				t.Errorf("invalid faculty scope for %s", user.email)
			}
		case "department":
			if user.facultyID != "" || user.deptID == "" {
				t.Errorf("invalid department scope for %s", user.email)
			}
		default:
			t.Errorf("invalid scope type %q for %s", user.scopeType, user.email)
		}
		if user.role == "lecturer" && user.staffNo != "" {
			lecturerStaff++
		}
	}
	for _, role := range []string{
		"platform_admin", "institution_admin", "faculty_admin", "department_admin", "lecturer", "student",
	} {
		if !roles[role] {
			t.Errorf("missing demo login for role %q", role)
		}
	}
	if lecturerStaff < 3 {
		t.Fatalf("lecturers with staff records=%d, want at least three", lecturerStaff)
	}
}

func TestSeedPlanIncludesOpenNowAndLaterTodayTimetable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC)
	plan, err := buildSeedPlan(now)
	if err != nil {
		t.Fatal(err)
	}

	location, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		t.Fatal(err)
	}
	localNow := now.In(location)
	var openEntry, laterEntry *seedTimetable
	for index := range plan.timetables {
		item := &plan.timetables[index]
		if item.offeringID == plan.offerings[0].id {
			openEntry = item
		}
		if item.offeringID == plan.offerings[1].id {
			laterEntry = item
		}
	}
	if openEntry == nil || laterEntry == nil {
		t.Fatal("missing today's open-now or later-today timetable entry")
	}
	if openEntry.weekday != int(localNow.Weekday()) || laterEntry.weekday != int(localNow.Weekday()) {
		t.Fatalf("today's timetable weekday mismatch: open=%d later=%d want=%d",
			openEntry.weekday, laterEntry.weekday, localNow.Weekday())
	}
	nowTime := localNow.Format("15:04:05")
	if !(openEntry.startsAt <= nowTime && nowTime < openEntry.endsAt) {
		t.Fatalf("open-now slot %s..%s does not include local time %s",
			openEntry.startsAt, openEntry.endsAt, nowTime)
	}
	if laterEntry.startsAt <= nowTime {
		t.Fatalf("later-today slot starts at %s, not after %s", laterEntry.startsAt, nowTime)
	}
	// Today's timetable still shows a class due now, but the seed leaves no
	// live session: starting one is the lecturer's job, and it is what gives
	// the room its coordinates.
	for _, session := range plan.sessions {
		if session.endedAt == nil {
			t.Errorf("demo session %s is open; the seed must leave no live classes", session.id)
		}
		if session.startedAt.After(now) {
			t.Errorf("demo session starts in the future: %s", session.startedAt)
		}
	}
	ranToday := 0
	for _, session := range plan.sessions {
		if session.startedAt.After(now.Add(-6 * time.Hour)) {
			ranToday++
		}
	}
	if ranToday == 0 {
		t.Error("no recently finished session, so the reports have nothing current to show")
	}
}

func TestSeedPlanTimetableRemainsValidLateInTheDay(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 22, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range plan.timetables {
		start, err := time.Parse("15:04:05", entry.startsAt)
		if err != nil {
			t.Fatal(err)
		}
		end, err := time.Parse("15:04:05", entry.endsAt)
		if err != nil {
			t.Fatal(err)
		}
		if !end.After(start) {
			t.Fatalf("invalid late-day timetable entry: %+v", entry)
		}
	}
}

func TestLegacyOpenSessionCleanupIsTenantScopedAndIdempotent(t *testing.T) {
	t.Parallel()
	if !strings.Contains(closeLegacyOpenSessionQuery, "institution_id=$1") ||
		!strings.Contains(closeLegacyOpenSessionQuery, "id=$2") ||
		!strings.Contains(closeLegacyOpenSessionQuery, "ended_at IS NULL") ||
		legacyOpenSessionID != "10000000-0000-4000-8000-000000001004" {
		t.Fatal("legacy cleanup must only close the known open demo fixture")
	}
}

func TestAttendanceFixturesCoverReportStatesAndBelowThreshold(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	sessionByID := make(map[string]seedSession, len(plan.sessions))
	for _, session := range plan.sessions {
		sessionByID[session.id] = session
	}
	webByKey := make(map[string]seedWebCheckin, len(plan.webCheckins))
	for _, checkin := range plan.webCheckins {
		webByKey[checkin.sessionID+":"+checkin.studentID] = checkin
	}
	scanByKey := make(map[string]bool, len(plan.fingerprintScans))
	for _, scan := range plan.fingerprintScans {
		scanByKey[scan.sessionID+":"+scan.studentID] = true
	}
	heartbeatByDevice := make(map[string]bool, len(plan.heartbeats))
	for _, heartbeat := range plan.heartbeats {
		heartbeatByDevice[heartbeat.deviceID] = true
	}
	outcomes := map[string]bool{}
	for _, session := range plan.sessions[:6] {
		studentIDs := map[string]bool{}
		for _, checkin := range plan.webCheckins {
			if checkin.sessionID == session.id {
				studentIDs[checkin.studentID] = true
			}
		}
		for _, scan := range plan.fingerprintScans {
			if scan.sessionID == session.id {
				studentIDs[scan.studentID] = true
			}
		}
		for studentID := range studentIDs {
			key := session.id + ":" + studentID
			_, hasWeb := webByKey[key]
			hasScan := scanByKey[key]
			switch {
			case hasWeb && hasScan:
				outcomes["verified"] = true
			case hasWeb && heartbeatByDevice[venueByID(plan.venues, session.venueID).device]:
				outcomes["web_only_no_fingerprint"] = true
			case hasScan && !hasWeb:
				outcomes["scan_only"] = true
			}
			if checkin, ok := webByKey[key]; ok && checkin.status == "late" {
				outcomes["late"] = true
			}
		}
	}
	for _, override := range plan.overrides {
		if override.status == "absent" {
			outcomes["absent"] = true
		}
		if override.status == "excused" && override.reason != "" {
			outcomes["override_reason"] = true
		}
	}
	if len(plan.sessions) > 7 {
		for _, checkin := range plan.webCheckins {
			if checkin.sessionID == plan.sessions[7].id && !scanByKey[checkin.sessionID+":"+checkin.studentID] {
				outcomes["web_only_unverified"] = true
			}
		}
	}
	for _, expected := range []string{
		"verified", "web_only_no_fingerprint", "scan_only", "absent", "late", "override_reason", "web_only_unverified",
	} {
		if !outcomes[expected] {
			t.Errorf("attendance fixture missing state %q (sessions found=%d)", expected, len(sessionByID))
		}
	}
	attended := 0
	for _, session := range plan.sessions[:6] {
		key := session.id + ":" + plan.students[0].id
		_, hasWeb := webByKey[key]
		if hasWeb && webByKey[key].status == "late" || scanByKey[key] {
			attended++
		}
	}
	if attended*100/6 >= 75 {
		t.Fatalf("history student attendance=%d/6, want below 75%%", attended)
	}
}

func TestSeedPlanIsDateRelativeAndDeterministic(t *testing.T) {
	t.Parallel()
	firstNow := time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC)
	secondNow := firstNow.AddDate(1, 0, 0)
	first, err := buildSeedPlan(firstNow)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := buildSeedPlan(firstNow)
	if err != nil {
		t.Fatal(err)
	}
	nextYear, err := buildSeedPlan(secondNow)
	if err != nil {
		t.Fatal(err)
	}
	if first.sessions[0].id != repeated.sessions[0].id ||
		first.students[0].matric != repeated.students[0].matric ||
		!first.sessions[0].startedAt.Equal(repeated.sessions[0].startedAt) {
		t.Fatal("identical inputs generated different demo fixtures")
	}
	if first.sessions[0].startedAt.Year() == nextYear.sessions[0].startedAt.Year() {
		t.Fatal("historical session timestamps did not move with the input date")
	}
	if first.academicStart.After(first.now) || first.academicEnd.Before(first.now) ||
		first.semesterStart.After(first.now) || first.semesterEnd.Before(first.now) {
		t.Fatal("current academic periods do not contain the supplied date")
	}
}

func TestSeedPlanProvidesPriorAndCurrentSemesters(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// The current semester must contain today; the prior semester must be
	// entirely in the past and precede the current one.
	if plan.semesterStart.After(plan.now) || plan.semesterEnd.Before(plan.now) {
		t.Fatalf("current semester %s..%s does not contain %s",
			plan.semesterStart, plan.semesterEnd, plan.now)
	}
	if !plan.priorSemesterEnd.Before(plan.now) {
		t.Fatalf("prior semester end %s is not before %s", plan.priorSemesterEnd, plan.now)
	}
	if !plan.priorSemesterEnd.Before(plan.semesterStart) {
		t.Fatalf("prior semester %s..%s overlaps the current semester starting %s",
			plan.priorSemesterStart, plan.priorSemesterEnd, plan.semesterStart)
	}
	// Both semesters must fall within the current academic session.
	if plan.priorSemesterStart.Before(plan.academicStart) || plan.semesterEnd.After(plan.academicEnd) {
		t.Fatalf("semesters fall outside the academic session %s..%s",
			plan.academicStart, plan.academicEnd)
	}
}

// Seeded venues and historical session snapshots stay unlocated so the first
// live class at each venue can capture the lecturer's device location.
func TestSeedCheckinsDoNotInventLocationAndUseDeviceBinding(t *testing.T) {
	t.Parallel()
	plan, err := buildSeedPlan(time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.webCheckins) == 0 {
		t.Fatal("seed has no web check-ins")
	}
	withoutPosition := 0
	for _, checkin := range plan.webCheckins {
		session := sessionByID(plan.sessions, checkin.sessionID)
		if session.id == "" {
			t.Fatalf("check-in references unknown session %s", checkin.sessionID)
		}
		if checkin.position == nil {
			withoutPosition++
			continue
		}
		t.Errorf("seed check-in for session %s has a fabricated location: %+v",
			session.id, checkin.position)
	}
	if withoutPosition == 0 {
		t.Error("no check-in was recorded without a position; a denied permission must still be demo-able")
	}
	hashes := make(map[string]bool, len(plan.webCheckins))
	for _, checkin := range plan.webCheckins {
		hash := studentDeviceHash(checkin.studentID)
		if len(hash) != 32 {
			t.Fatalf("device hash for %s is %d bytes, want 32", checkin.studentID, len(hash))
		}
		hashes[string(hash)] = true
	}
	if len(hashes) < 2 {
		t.Error("seeded check-ins share one device hash, which the binding rule would refuse")
	}
}

// Rerunning the seed must leave no live class behind, including one a lecturer
// started during an earlier demo. A session left open would occupy its venue
// and the one-open-session-per-venue rule would then refuse the class the demo
// asks the lecturer to start.
func TestCloseOpenDemoSessionsQueryIsTenantScopedAndOnlyTouchesOpenRows(t *testing.T) {
	t.Parallel()
	if !strings.Contains(closeOpenDemoSessionsQuery, "institution_id=$1") {
		t.Error("closing open demo sessions must be scoped to the demo institution")
	}
	if !strings.Contains(closeOpenDemoSessionsQuery, "ended_at IS NULL") {
		t.Error("closing open demo sessions must not rewrite an already closed session's end time")
	}
	if strings.Contains(closeOpenDemoSessionsQuery, "DELETE") {
		t.Error("open sessions are closed, never deleted; their attendance is a record")
	}
}
