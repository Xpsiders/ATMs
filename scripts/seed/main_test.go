package main

import (
	"math"
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
		len(plan.courses) != 10 || len(plan.offerings) != 10 || len(plan.venues) != 4 {
		t.Fatalf("unexpected organization/catalogue counts: faculties=%d departments=%d programmes=%d courses=%d offerings=%d venues=%d",
			len(plan.faculties), len(plan.departments), len(plan.programmes),
			len(plan.courses), len(plan.offerings), len(plan.venues))
	}
	if len(plan.students) != 48 {
		t.Fatalf("student records=%d, want 48", len(plan.students))
	}
	claimed, unclaimed := 0, 0
	for _, student := range plan.students {
		if student.userID == "" {
			unclaimed++
		} else {
			claimed++
		}
	}
	if claimed != 16 || unclaimed != 32 {
		t.Fatalf("claimed=%d unclaimed=%d, want 16 and 32", claimed, unclaimed)
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

func TestMetresToDegreeOffsetsAcrossLatitudes(t *testing.T) {
	t.Parallel()
	for _, latitude := range []float64{0, 45, 80} {
		north, east, err := metresToDegreeOffsets(latitude, 100, 100)
		if err != nil {
			t.Fatalf("latitude %v: %v", latitude, err)
		}
		if math.Abs(north*111320-100) > 0.001 {
			t.Errorf("latitude %v: north offset represents %.3f m, want 100", latitude, north*111320)
		}
		eastMeters := east * 111320 * math.Cos(latitude*math.Pi/180)
		if math.Abs(eastMeters-100) > 0.001 {
			t.Errorf("latitude %v: east offset represents %.3f m, want 100", latitude, eastMeters)
		}
	}
	if _, _, err := metresToDegreeOffsets(90.1, 0, 0); err == nil {
		t.Fatal("expected invalid latitude to be rejected")
	}
}

func TestLoadSeedConfigDefaultsParsesAndValidates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		values  map[string]string
		want    seedConfig
		wantErr string
	}{
		{
			name: "defaults",
			want: seedConfig{originLatitude: defaultOriginLatitude, originLongitude: defaultOriginLongitude, venueRadius: defaultVenueRadius},
		},
		{
			name: "configured",
			values: map[string]string{
				"SEED_ORIGIN_LAT": " -33.8688 ", "SEED_ORIGIN_LNG": "151.2093",
				"SEED_VENUE_RADIUS_M": "200",
			},
			want: seedConfig{originLatitude: -33.8688, originLongitude: 151.2093, venueRadius: 200, originConfigured: true},
		},
		{
			name: "valid boundaries",
			values: map[string]string{
				"SEED_ORIGIN_LAT": "-90", "SEED_ORIGIN_LNG": "180",
				"SEED_VENUE_RADIUS_M": "20",
			},
			want: seedConfig{originLatitude: -90, originLongitude: 180, venueRadius: 20, originConfigured: true},
		},
		{
			name: "valid upper radius",
			values: map[string]string{
				"SEED_ORIGIN_LAT": "90", "SEED_ORIGIN_LNG": "-180",
				"SEED_VENUE_RADIUS_M": "2000",
			},
			want: seedConfig{originLatitude: 90, originLongitude: -180, venueRadius: 2000, originConfigured: true},
		},
		{name: "latitude out of range", values: map[string]string{"SEED_ORIGIN_LAT": "90.1", "SEED_ORIGIN_LNG": "0"}, wantErr: "SEED_ORIGIN_LAT"},
		{name: "longitude out of range", values: map[string]string{"SEED_ORIGIN_LAT": "0", "SEED_ORIGIN_LNG": "-180.1"}, wantErr: "SEED_ORIGIN_LNG"},
		{name: "invalid latitude", values: map[string]string{"SEED_ORIGIN_LAT": "north", "SEED_ORIGIN_LNG": "0"}, wantErr: "SEED_ORIGIN_LAT"},
		{name: "partial origin", values: map[string]string{"SEED_ORIGIN_LAT": "1"}, wantErr: "must be set together"},
		{name: "radius below range", values: map[string]string{"SEED_VENUE_RADIUS_M": "19"}, wantErr: "SEED_VENUE_RADIUS_M"},
		{name: "radius above range", values: map[string]string{"SEED_VENUE_RADIUS_M": "2001"}, wantErr: "SEED_VENUE_RADIUS_M"},
		{name: "invalid radius", values: map[string]string{"SEED_VENUE_RADIUS_M": "wide"}, wantErr: "SEED_VENUE_RADIUS_M"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(key string) string { return test.values[key] }
			got, err := loadSeedConfig(getenv)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("loadSeedConfig error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("loadSeedConfig = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestSeedVenuePlacementAndOpenSessionSnapshotsUseConfiguredOrigin(t *testing.T) {
	t.Parallel()
	config := seedConfig{originLatitude: 6.5, originLongitude: 3.3, venueRadius: 200, originConfigured: true}
	venues, err := placeSeedVenues(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(venues) != 4 {
		t.Fatalf("venue count = %d, want 4", len(venues))
	}
	if venues[0].id != seedID(400) || venues[0].lat != config.originLatitude ||
		venues[0].lng != config.originLongitude || venues[0].radius != config.venueRadius {
		t.Fatalf("origin venue mismatch: %+v", venues[0])
	}
	for _, venue := range venues[1:3] {
		if distanceMeters(config.originLatitude, config.originLongitude, venue.lat, venue.lng) > 30 {
			t.Errorf("near venue %q is not within 30m of configured origin", venue.name)
		}
		if venue.radius != config.venueRadius {
			t.Errorf("near venue %q radius = %d, want %d", venue.name, venue.radius, config.venueRadius)
		}
	}
	farVenue := venues[3]
	if farVenue.id != seedID(403) || farVenue.name != "Far Test Venue (5 km)" ||
		math.Abs(distanceMeters(config.originLatitude, config.originLongitude, farVenue.lat, farVenue.lng)-5000) > 10 ||
		farVenue.radius != 60 {
		t.Fatalf("far venue mismatch: %+v", farVenue)
	}

	plan, err := buildSeedPlanWithConfig(
		time.Date(2026, time.October, 21, 7, 53, 0, 0, time.UTC), config)
	if err != nil {
		t.Fatal(err)
	}
	venueByID := make(map[string]seedVenue, len(plan.venues))
	for _, venue := range plan.venues {
		venueByID[venue.id] = venue
	}
	openCount := 0
	for _, session := range plan.sessions {
		if session.endedAt != nil {
			continue
		}
		openCount++
		venue := venueByID[session.venueID]
		if session.latitude != venue.lat || session.longitude != venue.lng || session.radius != venue.radius {
			t.Errorf("open session %s snapshot does not match venue %s: %+v / %+v",
				session.id, venue.id, session, venue)
		}
	}
	if openCount == 0 {
		t.Fatal("seed has no open session to check")
	}
}

func distanceMeters(firstLat, firstLng, secondLat, secondLng float64) float64 {
	const earthRadiusMeters = 6371000
	lat1, lat2 := firstLat*math.Pi/180, secondLat*math.Pi/180
	dLat := (secondLat - firstLat) * math.Pi / 180
	dLng := (secondLng - firstLng) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return earthRadiusMeters * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
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
	for _, lecturer := range plan.users {
		if lecturer.role == "lecturer" {
			lecturerDepartments[lecturer.id] = lecturer.deptID
		}
	}
	for _, offering := range plan.offerings {
		if lecturerDepartments[offering.lecturerID] != courseDepartments[offering.courseID] {
			t.Errorf("lecturer %s from department %s is assigned course %s owned by department %s",
				offering.lecturerID, lecturerDepartments[offering.lecturerID],
				offering.courseID, courseDepartments[offering.courseID])
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
	openSessions := 0
	for _, session := range plan.sessions {
		if session.endedAt == nil {
			openSessions++
			if session.startedAt.After(now) {
				t.Errorf("open demo session starts in the future: %s", session.startedAt)
			}
		}
	}
	if openSessions != 2 {
		t.Fatalf("open sessions=%d, want one now at each of two venues", openSessions)
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
				outcomes["suspected_proxy"] = true
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
		"verified", "suspected_proxy", "scan_only", "absent", "late", "override_reason", "web_only_unverified",
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
