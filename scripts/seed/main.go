package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"attendance-api/internal/scans"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const institutionID = "10000000-0000-4000-8000-000000000001"
const legacyOpenSessionID = "10000000-0000-4000-8000-000000001004"
const closeLegacyOpenSessionQuery = `UPDATE lecture_sessions SET ended_at=$3
WHERE institution_id=$1 AND id=$2 AND ended_at IS NULL`

// closeOpenDemoSessionsQuery leaves the demo institution with no live class.
// Starting one is the lecturer's job and is what calibrates the room, so the
// seed must not leave a session occupying a venue - the
// one-open-session-per-venue rule would then refuse the very class the demo
// asks the lecturer to start. It is scoped to the demo institution, and only
// touches sessions still open.
const closeOpenDemoSessionsQuery = `UPDATE lecture_sessions SET ended_at=$2
WHERE institution_id=$1 AND ended_at IS NULL`

var (
	seedLecturerNames = []string{
		"Dr. Ada Okafor",
		"Mr. Tunde Bello",
		"Prof. Ngozi Eze",
		"Dr. Sola Adeyemi",
		"Dr. Chinedu Nwosu",
		"Mrs. Folake Ojo",
		"Prof. Ibrahim Sani",
		"Dr. Yusuf Danladi",
		"Dr. Amaka Obi",
		"Mr. Segun Afolabi",
		"Dr. Halima Bako",
		"Prof. Chidi Okeke",
		"Mrs. Bisi Lawal",
		"Dr. Emeka Udeh",
	}
	seedStudentFirstNames = []string{
		"Amara", "Chika", "Emeka", "Femi", "Ifeoma", "Kemi",
		"Kunle", "Mariam", "Nneka", "Olu", "Tari", "Zainab",
	}
	// Five surnames against twelve first names gives exactly sixty unique
	// student names, one per seeded record.
	seedStudentLastNames = []string{"Adebayo", "Balogun", "Eze", "Okafor", "Ogunleye"}
)

type seedPlan struct {
	now                time.Time
	academicStart      time.Time
	academicEnd        time.Time
	priorSemesterStart time.Time
	priorSemesterEnd   time.Time
	semesterStart      time.Time
	semesterEnd        time.Time
	faculties          []seedFaculty
	departments        []seedDepartment
	programmes         []seedProgramme
	users              []seedUser
	students           []seedStudent
	courses            []seedCourse
	offerings          []seedOffering
	venues             []seedVenue
	timetables         []seedTimetable
	sessions           []seedSession
	registrations      []seedRegistration
	webCheckins        []seedWebCheckin
	fingerprintScans   []seedFingerprint
	overrides          []seedOverride
	heartbeats         []seedHeartbeat
}

type seedFaculty struct {
	id   string
	name string
}

type seedDepartment struct {
	id        string
	facultyID string
	name      string
	code      string
}

type seedProgramme struct {
	id           string
	departmentID string
	name         string
	code         string
}

type seedUser struct {
	id        string
	label     string
	email     string
	password  string
	role      string
	scopeType string
	facultyID string
	deptID    string
	staffNo   string
}

type seedStudent struct {
	id           string
	matric       string
	email        string
	name         string
	departmentID string
	programmeID  string
	level        int
	userID       string
}

type seedCourse struct {
	id           string
	departmentID string
	code         string
	title        string
	kind         string
	description  string
}

type seedOffering struct {
	id             string
	courseID       string
	level          int
	mode           string
	lecturerID     string
	minimumPercent float64
	lateCounts     bool
	autoTargets    []string
}

type seedVenue struct {
	id     string
	name   string
	lat    float64
	lng    float64
	radius int
	device string
	// located is false for a room that has never been stood in. Its
	// coordinates and radius are stored as NULL, which is now the normal
	// starting state for a venue: the first lecturer to teach there supplies
	// the position from their own device. The surviving
	// CHECK (radius_meters > 0) also means a radius of 0 is not a legal
	// stand-in for "unknown".
	located bool
}

type seedTimetable struct {
	id         string
	offeringID string
	venueID    string
	weekday    int
	startsAt   string
	endsAt     string
}

type seedSession struct {
	id         string
	offeringID string
	venueID    string
	timetable  string
	lecturerID string
	startedAt  time.Time
	endedAt    *time.Time
	latitude   float64
	longitude  float64
	radius     int
	lateAfter  int
	// located mirrors seedVenue.located: a session started at an
	// uncalibrated room snapshots no geofence, and check-ins against it carry
	// no distance.
	located bool
}

type seedRegistration struct {
	offeringID string
	studentID  string
	status     string
}

type seedWebCheckin struct {
	sessionID string
	studentID string
	status    string
	at        time.Time
	onRoster  bool
	// position is nil when the student's browser supplied no location. That is
	// an ordinary successful check-in now: location is evidence, never a gate.
	position *seedPosition
}

// seedPosition is the fix a web check-in was recorded with. distance is stored
// rather than derived so a fixture can describe a position the phone's own
// accuracy cannot explain, which the session report flags as
// location_unverified.
type seedPosition struct {
	latitude  float64
	longitude float64
	accuracy  float64
	distance  float64
}

type seedFingerprint struct {
	sessionID string
	studentID string
	deviceID  string
	at        time.Time
}

type seedOverride struct {
	sessionID string
	studentID string
	status    string
	reason    string
	code      string
	note      string
	actorID   string
	at        time.Time
}

type seedHeartbeat struct {
	deviceID string
	at       time.Time
}

type seedDeviceSlot struct {
	id        string
	deviceID  string
	studentID string
	number    int
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	reset := flags.Bool("reset", false, "delete and recreate only the demo institution")
	updateVenues := flags.Bool("update-venues", false, "update seeded venue coordinates and open session snapshots without resetting data")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *reset && *updateVenues {
		return errors.New("--reset and --update-venues cannot be used together")
	}
	if flags.NArg() != 0 {
		return errors.New("seed accepts only the --reset and --update-venues flags")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	now := time.Now().UTC()
	plan, err := buildSeedPlan(now)
	if err != nil {
		return err
	}
	if *updateVenues {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin venue update transaction: %w", err)
		}
		defer tx.Rollback(ctx)
		if err := updateSeedVenues(ctx, tx, plan); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit venue update: %w", err)
		}
		printVenueConfiguration(plan)
		return nil
	}
	passwordHashes := make(map[string][]byte, len(plan.users))
	for _, user := range plan.users {
		hash, err := bcrypt.GenerateFromPassword([]byte(user.password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash demo password for %s: %w", user.label, err)
		}
		passwordHashes[user.id] = hash
	}
	deviceKeys := make(map[string]string, len(plan.venues))
	deviceHashes := make(map[string][]byte, len(plan.venues))
	for _, venue := range plan.venues {
		key, hash, err := scans.GenerateDeviceKey(nil)
		if err != nil {
			return fmt.Errorf("generate demo scanner key for %s: %w", venue.name, err)
		}
		deviceKeys[venue.device] = key
		deviceHashes[venue.device] = hash
	}
	invitationToken, _, err := scans.GenerateDeviceKey(nil)
	if err != nil {
		return fmt.Errorf("generate pending invitation token: %w", err)
	}
	invitationHash := sha256.Sum256([]byte(invitationToken))
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	if *reset {
		if err := resetDemoInstitution(ctx, tx); err != nil {
			return err
		}
	}
	createdDevices, invitationCreated, err := seedDemoData(ctx, tx, plan, passwordHashes, deviceHashes, invitationHash[:])
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit demo data: %w", err)
	}
	printCredentials(plan.users, plan.students)
	if len(createdDevices) > 0 {
		fmt.Println("\nRegistered scanner keys (shown only on initial creation):")
		for _, venue := range plan.venues {
			if createdDevices[venue.device] {
				fmt.Printf("  %s: %s\n", venue.name, deviceKeys[venue.device])
			}
		}
	} else {
		fmt.Println("\nExisting demo scanner keys were preserved and are not displayed.")
	}
	if invitationCreated {
		fmt.Printf("\nPending invitation token (shown only on initial creation): %s\n", invitationToken)
	}
	if !*reset {
		fmt.Println("Use --reset to recreate only this demo institution and generate new scanner keys.")
	}
	printVenueConfiguration(plan)
	return nil
}

func buildSeedPlan(now time.Time) (seedPlan, error) {
	loc, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return seedPlan{}, fmt.Errorf("load Africa/Lagos timezone: %w", err)
	}
	now = now.UTC()
	localNow := now.In(loc)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	plan := seedPlan{
		now:                now,
		academicStart:      today.AddDate(0, 0, -170),
		academicEnd:        today.AddDate(0, 0, 275),
		priorSemesterStart: today.AddDate(0, 0, -150),
		priorSemesterEnd:   today.AddDate(0, 0, -40),
		semesterStart:      today.AddDate(0, 0, -30),
		semesterEnd:        today.AddDate(0, 0, 150),
		faculties: []seedFaculty{
			{id: seedID(2), name: "Faculty of Engineering"},
			{id: seedID(3), name: "Faculty of Science"},
		},
		departments: []seedDepartment{
			{id: seedID(4), facultyID: seedID(2), name: "Electrical and Computer Engineering", code: "ECE"},
			{id: seedID(5), facultyID: seedID(2), name: "Mechanical Engineering", code: "ME"},
			{id: seedID(6), facultyID: seedID(3), name: "Computer Science", code: "CSC"},
			{id: seedID(7), facultyID: seedID(3), name: "Mathematics", code: "MTH"},
		},
		programmes: []seedProgramme{
			{id: seedID(8), departmentID: seedID(4), name: "Computer Engineering", code: "CPE"},
			{id: seedID(9), departmentID: seedID(5), name: "Mechanical Engineering", code: "MEC"},
			{id: seedID(10), departmentID: seedID(6), name: "Computer Science", code: "CSC"},
			{id: seedID(11), departmentID: seedID(7), name: "Applied Mathematics", code: "AMT"},
		},
	}
	credentialID := 20
	addUser := func(label, email, role, scope, facultyID, departmentID, staffNo string) string {
		id := seedID(credentialID)
		credentialID++
		plan.users = append(plan.users, seedUser{
			id: id, label: label, email: email, password: "DemoPass-2026!", role: role,
			scopeType: scope, facultyID: facultyID, deptID: departmentID, staffNo: staffNo,
		})
		return id
	}
	addUser("Platform administrator", "platform-admin@attendance.test", "platform_admin", "platform", "", "", "")
	addUser("Institution administrator", "admin@attendance.test", "institution_admin", "institution", "", "", "")
	addUser("Engineering faculty administrator", "engineering-admin@attendance.test", "faculty_admin", "faculty", seedID(2), "", "")
	addUser("Science faculty administrator", "science-admin@attendance.test", "faculty_admin", "faculty", seedID(3), "", "")
	lecturerIDs := make([]string, len(plan.departments))
	lecturerName := 0
	nextLecturerName := func() string {
		name := seedLecturerNames[lecturerName%len(seedLecturerNames)]
		lecturerName++
		return name
	}
	departmentLecturers := make([]int, len(plan.departments))
	// Every lecturer in a department, so the extra courses below can be shared
	// out instead of piling onto the one lecturer per department.
	lecturersByDepartment := make([][]string, len(plan.departments))
	addLecturer := func(departmentIndex int) string {
		department := plan.departments[departmentIndex]
		departmentLecturers[departmentIndex]++
		ordinal := departmentLecturers[departmentIndex]
		email := strings.ToLower(department.code) + "-lecturer@attendance.test"
		if ordinal > 1 {
			email = strings.ToLower(fmt.Sprintf("%s-lecturer-%02d@attendance.test", department.code, ordinal))
		}
		id := addUser(nextLecturerName(), email, "lecturer", "department", "", department.id,
			fmt.Sprintf("STAFF-%s-%02d", department.code, ordinal))
		lecturersByDepartment[departmentIndex] = append(lecturersByDepartment[departmentIndex], id)
		return id
	}
	for index, department := range plan.departments {
		addUser(department.name+" administrator",
			strings.ToLower(department.code)+"-admin@attendance.test",
			"department_admin", "department", "", department.id, "")
		lecturerIDs[index] = addLecturer(index)
	}
	// Additional unassigned lecturers spread across departments so the lecturer
	// picker (GET /users?role=lecturer) has more than one option per department.
	for i := 0; i < 10; i++ {
		addLecturer(i % len(plan.departments))
	}
	studentNumber := 100
	for departmentIndex, department := range plan.departments {
		programmeID := plan.programmes[departmentIndex].id
		for levelIndex, level := range []int{100, 200, 300, 400, 500} {
			for ordinal := 0; ordinal < 3; ordinal++ {
				studentID := seedID(studentNumber)
				studentNumber++
				matric := fmt.Sprintf("%s-%d-%02d", department.code, level, ordinal+1)
				email := strings.ToLower(fmt.Sprintf("%s-%d-%02d@student.attendance.test", department.code, level, ordinal+1))
				displayName := seedStudentDisplayName(departmentIndex, levelIndex, ordinal)
				userID := ""
				if ordinal < 2 {
					userID = addUser(
						displayName,
						email, "student", "department", "", department.id, "")
				}
				plan.students = append(plan.students, seedStudent{
					id: studentID, matric: matric, email: email,
					name:         displayName,
					departmentID: department.id, programmeID: programmeID, level: level, userID: userID,
				})
			}
		}
	}
	courseSpecs := []seedCourse{
		{id: seedID(200), departmentID: seedID(4), code: "ECE101", title: "Introduction to Electrical Engineering", kind: "departmental", description: "Core electrical engineering principles and circuit fundamentals"},
		{id: seedID(201), departmentID: seedID(4), code: "ECE201", title: "Digital Logic Design", kind: "departmental", description: "Elective: digital systems, logic design, and computer architecture"},
		{id: seedID(202), departmentID: seedID(4), code: "ECE301", title: "Signals and Systems", kind: "departmental", description: "Signals, systems, and transforms"},
		{id: seedID(203), departmentID: seedID(4), code: "ECE401", title: "Embedded Systems", kind: "departmental", description: "Microcontrollers, embedded software, and connected devices"},
		{id: seedID(204), departmentID: seedID(5), code: "MEE101", title: "Engineering Mechanics", kind: "departmental", description: "Statics, dynamics, and engineering mechanics"},
		{id: seedID(205), departmentID: seedID(5), code: "MEE201", title: "Thermodynamics", kind: "departmental", description: "Energy, heat, and thermodynamic processes"},
		{id: seedID(206), departmentID: seedID(6), code: "CSC101", title: "Programming Fundamentals", kind: "departmental", description: "Programming and computational thinking"},
		{id: seedID(207), departmentID: seedID(6), code: "CSC301", title: "Database Systems", kind: "departmental", description: "Relational database design and implementation"},
		{id: seedID(208), departmentID: seedID(4), code: "GST101", title: "Communication Skills", kind: "general", description: "Academic writing and communication for all faculties"},
		{id: seedID(209), departmentID: seedID(7), code: "MTH201", title: "Applied Statistics", kind: "general", description: "Probability and applied statistical methods"},
	}
	plan.courses = courseSpecs
	modes := []string{"auto", "approval", "open", "closed", "auto", "approval", "open", "auto", "open", "approval"}
	levels := []int{100, 200, 300, 400, 100, 200, 100, 300, 100, 200}
	departmentIndexes := []int{0, 0, 0, 0, 1, 1, 2, 2, 0, 3}
	for index, course := range plan.courses {
		lecturerID := lecturerIDs[departmentIndexes[index]]
		offering := seedOffering{
			id: seedID(300 + index), courseID: course.id, level: levels[index],
			mode: modes[index], lecturerID: lecturerID, minimumPercent: 75, lateCounts: true,
		}
		if course.kind == "general" && offering.mode == "auto" {
			offering.autoTargets = departmentIDs(plan.departments)
		}
		plan.offerings = append(plan.offerings, offering)
	}
	appendCatalogueDepth(&plan, lecturersByDepartment)
	venues := placeSeedVenues()
	plan.venues = venues
	weekDay := int(localNow.Weekday())
	currentStart := localNow.Add(-30 * time.Minute).Truncate(time.Minute)
	if currentStart.Before(today) {
		currentStart = today
	}
	currentEnd := localNow.Add(90 * time.Minute).Truncate(time.Minute)
	if currentEnd.Day() != localNow.Day() {
		currentEnd = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 59, 0, 0, loc)
	}
	laterStart := localNow.Add(2 * time.Hour).Truncate(time.Minute)
	laterWeekday := weekDay
	if laterStart.Day() != localNow.Day() {
		laterStart = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 0, 0, 0, loc)
	}
	if !laterStart.After(localNow) {
		laterStart = localNow.Add(time.Minute).Truncate(time.Minute)
		if laterStart.Day() != localNow.Day() {
			laterWeekday = (weekDay + 1) % 7
		}
	}
	laterEnd := laterStart.Add(90 * time.Minute)
	if laterStart.Day() == localNow.Day() && laterEnd.Day() != localNow.Day() {
		laterEnd = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 59, 0, 0, loc)
	}
	currentSlot := func(offerIndex int, venueIndex int, start, end time.Time) seedTimetable {
		return seedTimetable{
			id: seedID(500 + offerIndex), offeringID: plan.offerings[offerIndex].id,
			venueID: venues[venueIndex].id, weekday: weekDay,
			startsAt: start.In(loc).Format("15:04:05"), endsAt: end.In(loc).Format("15:04:05"),
		}
	}
	laterSlot := currentSlot(1, 2, laterStart, laterEnd)
	laterSlot.weekday = laterWeekday
	plan.timetables = append(plan.timetables,
		currentSlot(0, 0, currentStart, currentEnd),
		currentSlot(8, 1, currentStart, currentEnd),
		laterSlot)
	for index := 2; index < len(plan.offerings); index++ {
		weekday := (weekDay + index + 1) % 7
		startHour := 8 + (index%5)*2
		plan.timetables = append(plan.timetables, seedTimetable{
			id: seedID(500 + index), offeringID: plan.offerings[index].id,
			venueID: venues[index%len(venues)].id, weekday: weekday,
			startsAt: fmt.Sprintf("%02d:00:00", startHour),
			endsAt:   fmt.Sprintf("%02d:30:00", startHour+1),
		})
	}
	for index, item := range plan.timetables {
		offer := offeringByID(plan.offerings, item.offeringID)
		plan.timetables[index].id = seedID(500 + offeringIndex(plan.offerings, offer.id))
	}
	sessionID := 600
	for _, past := range []struct {
		daysAgo int
		start   time.Duration
	}{
		{21, 8 * time.Hour}, {18, 8 * time.Hour}, {14, 8 * time.Hour},
		{11, 8 * time.Hour}, {7, 8 * time.Hour}, {4, 8 * time.Hour},
	} {
		timetable := plan.timetables[0]
		venue := venueByID(venues, timetable.venueID)
		started := today.AddDate(0, 0, -past.daysAgo).Add(past.start).UTC()
		ended := started.Add(75 * time.Minute)
		plan.sessions = append(plan.sessions, seedSession{
			id: seedID(sessionID), offeringID: timetable.offeringID, venueID: timetable.venueID,
			timetable: timetable.id, lecturerID: plan.offerings[0].lecturerID,
			startedAt: started, endedAt: &ended, latitude: venue.lat, longitude: venue.lng,
			radius: venue.radius, lateAfter: 10, located: venue.located,
		})
		sessionID++
	}
	// Nothing is left open. A live class is something a lecturer starts from
	// their own device, which is also what calibrates the room, so seeding an
	// already-open session would both pre-empt that and block the venue under
	// the one-open-session-per-venue rule. These two ended a few minutes ago,
	// so today's timetable still shows a class that has just run.
	justEnded := currentStart.Add(25 * time.Minute).UTC()
	if !justEnded.Before(plan.now) {
		justEnded = plan.now.Add(-time.Minute)
	}
	for index := 0; index < 2; index++ {
		timetable := plan.timetables[0]
		venue := venueByID(venues, timetable.venueID)
		started := currentStart.UTC()
		ended := justEnded
		if !ended.After(started) {
			ended = started.Add(time.Minute)
		}
		plan.sessions = append(plan.sessions, seedSession{
			id: seedID(sessionID), offeringID: timetable.offeringID, venueID: timetable.venueID,
			timetable: timetable.id, lecturerID: plan.offerings[offeringIndex(plan.offerings, timetable.offeringID)].lecturerID,
			startedAt: started, endedAt: &ended, latitude: venue.lat, longitude: venue.lng,
			radius: venue.radius, lateAfter: 10, located: venue.located,
		})
		sessionID++
	}
	// Keep the final historical fixture at the primary venue too. Other
	// venues stay empty so a lecturer can start a class there during the demo.
	timetable := plan.timetables[0]
	started := today.AddDate(0, 0, -2).Add(10 * time.Hour).UTC()
	ended := started.Add(75 * time.Minute)
	plan.sessions = append(plan.sessions, seedSession{
		id: seedID(sessionID), offeringID: timetable.offeringID, venueID: timetable.venueID,
		timetable: timetable.id, lecturerID: plan.offerings[0].lecturerID,
		startedAt: started, endedAt: &ended, lateAfter: 10,
	})

	buildRegistrations(&plan)
	buildAttendance(&plan)
	if err := plan.validate(); err != nil {
		return seedPlan{}, err
	}
	return plan, nil
}

// appendCatalogueDepth adds the bulk of the catalogue: fifty further courses
// spread over 200 to 500 level, twenty of them general-studies courses open to
// every department, each with an offering and a lecturer from the owning
// department. The first ten courses stay hand-written because the demo script
// and the tests name them; these exist so search, filtering, the lecturer
// picker and the registration screens have a realistic amount to work with.
func appendCatalogueDepth(plan *seedPlan, lecturersByDepartment [][]string) {
	const (
		generalCourses      = 20
		departmentalCourses = 30
	)
	levels := []int{200, 300, 400, 500}
	// Mostly registerable modes, with closed appearing occasionally so the
	// "registration is not open" path has examples at several levels.
	modes := []string{"auto", "open", "approval", "auto", "open", "auto", "approval", "closed"}
	generalTitles := []string{
		"Use of English", "Nigerian Peoples and Culture", "Philosophy and Logic",
		"Entrepreneurship Studies", "Environment and Sustainability",
		"History of Science and Technology", "Peace and Conflict Studies",
		"Citizenship Education", "Research Methods", "Technical Writing",
		"Introduction to Economics", "Data Literacy", "Professional Ethics",
		"Public Health Awareness", "Leadership and Teamwork",
		"Digital Skills for Scholarship", "Statistics for Social Research",
		"Innovation and Enterprise", "Climate and Society", "Critical Thinking",
	}
	departmentalTitles := []string{
		"Circuit Analysis", "Control Systems", "Power Electronics",
		"Microprocessor Systems", "Communication Systems", "Machine Design",
		"Fluid Mechanics", "Heat Transfer", "Manufacturing Processes",
		"Applied Dynamics", "Data Structures", "Operating Systems",
		"Computer Networks", "Software Engineering", "Artificial Intelligence",
		"Compiler Construction", "Linear Algebra", "Real Analysis",
		"Numerical Methods", "Discrete Mathematics", "Optimisation Methods",
		"Instrumentation and Measurement", "Renewable Energy Systems",
		"Mechatronics", "Thermal Systems Design", "Mobile Application Development",
		"Information Security", "Cloud and Distributed Systems",
		"Stochastic Processes", "Mathematical Modelling",
	}
	codeOrdinals := make(map[string]int)
	nextCode := func(prefix string, level int) string {
		key := fmt.Sprintf("%s-%d", prefix, level)
		codeOrdinals[key]++
		// Ordinals start at 10 so no generated code can collide with the
		// hand-written ones, which all use 01.
		return fmt.Sprintf("%s%d%02d", prefix, level/100, 9+codeOrdinals[key])
	}
	lecturerTurn := make([]int, len(plan.departments))
	nextLecturer := func(departmentIndex int) string {
		lecturers := lecturersByDepartment[departmentIndex]
		if len(lecturers) == 0 {
			return ""
		}
		id := lecturers[lecturerTurn[departmentIndex]%len(lecturers)]
		lecturerTurn[departmentIndex]++
		return id
	}
	add := func(ordinal int, kind, title string) {
		departmentIndex := ordinal % len(plan.departments)
		department := plan.departments[departmentIndex]
		level := levels[(ordinal/len(plan.departments))%len(levels)]
		prefix := department.code
		description := title + " for " + department.name
		if kind == "general" {
			prefix = "GST"
			description = title + ": a general-studies course open to every department"
		}
		course := seedCourse{
			id: seedID(2000 + len(plan.courses)), departmentID: department.id,
			code: nextCode(prefix, level), title: title, kind: kind,
			description: description,
		}
		plan.courses = append(plan.courses, course)
		offering := seedOffering{
			id: seedID(2100 + len(plan.offerings)), courseID: course.id, level: level,
			mode: modes[ordinal%len(modes)], lecturerID: nextLecturer(departmentIndex),
			minimumPercent: 75, lateCounts: true,
		}
		if kind == "general" && offering.mode == "auto" {
			offering.autoTargets = departmentIDs(plan.departments)
		}
		plan.offerings = append(plan.offerings, offering)
	}
	for ordinal := 0; ordinal < generalCourses; ordinal++ {
		add(ordinal, "general", generalTitles[ordinal%len(generalTitles)])
	}
	for ordinal := 0; ordinal < departmentalCourses; ordinal++ {
		add(ordinal, "departmental", departmentalTitles[ordinal%len(departmentalTitles)])
	}
}

func seedStudentDisplayName(departmentIndex, levelIndex, ordinal int) string {
	// Fifteen records per department: five levels of three students.
	index := departmentIndex*15 + levelIndex*3 + ordinal
	return seedStudentFirstNames[index%len(seedStudentFirstNames)] + " " +
		seedStudentLastNames[index/len(seedStudentFirstNames)]
}

func placeSeedVenues() []seedVenue {
	return []seedVenue{
		{id: seedID(400), name: "LT 2", device: seedID(410)},
		{id: seedID(401), name: "Science Lecture Theatre", device: seedID(411)},
		{id: seedID(402), name: "Innovation Lab", device: seedID(412)},
		{id: seedID(403), name: "Engineering Lab", device: seedID(413)},
	}
}

func updateSeedVenues(ctx context.Context, tx pgx.Tx, plan seedPlan) error {
	for _, venue := range plan.venues {
		latitude, longitude, radius := venueLocationArgs(venue)
		if err := execSeed(ctx, tx, "update venue "+venue.name, `
INSERT INTO venues(id,institution_id,name,latitude,longitude,radius_meters)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(institution_id,id) DO UPDATE SET name=EXCLUDED.name,
latitude=EXCLUDED.latitude,longitude=EXCLUDED.longitude,radius_meters=EXCLUDED.radius_meters`,
			venue.id, institutionID, venue.name, latitude, longitude, radius); err != nil {
			return err
		}
		// Any session a lecturer left open at this venue is re-snapshotted so a
		// moved origin does not strand its students. A venue with no position
		// clears the snapshot instead of writing zeroes.
		if err := execSeed(ctx, tx, "update open sessions for "+venue.name, `
UPDATE lecture_sessions AS ls
SET latitude=v.latitude,longitude=v.longitude,radius_meters=v.radius_meters
FROM venues AS v
WHERE ls.institution_id=$1 AND ls.venue_id=$2 AND ls.ended_at IS NULL
  AND v.institution_id=ls.institution_id AND v.id=ls.venue_id`,
			institutionID, venue.id); err != nil {
			return err
		}
	}
	return nil
}

func buildRegistrations(plan *seedPlan) {
	for _, offering := range plan.offerings {
		course := courseByID(plan.courses, offering.courseID)
		eligible := 0
		for _, student := range plan.students {
			if student.userID == "" {
				continue
			}
			if offering.level != student.level ||
				course.kind == "departmental" && course.departmentID != student.departmentID ||
				course.kind == "general" && !contains(offering.autoTargets, student.departmentID) && offering.mode == "auto" {
				continue
			}
			status := ""
			switch offering.mode {
			case "auto":
				if course.kind == "departmental" || contains(offering.autoTargets, student.departmentID) {
					status = "approved"
				} else {
					status = "pending"
				}
			case "open":
				// Half the eligible students have taken an open offering up.
				if eligible%2 == 0 {
					status = "approved"
				}
			case "approval":
				// The first is already approved and the rest are waiting, so
				// every approval offering shows both sides of the queue.
				if eligible == 0 {
					status = "approved"
				} else {
					status = "pending"
				}
			}
			eligible++
			if status != "" {
				plan.registrations = append(plan.registrations, seedRegistration{
					offeringID: offering.id, studentID: student.id, status: status,
				})
			}
		}
	}
}

func buildAttendance(plan *seedPlan) {
	students := make([]seedStudent, 0, 3)
	for _, student := range plan.students {
		if student.departmentID == plan.departments[0].id && student.level == 100 {
			students = append(students, student)
		}
	}
	if len(students) < 3 {
		return
	}
	attendanceSessions := plan.sessions[:6]
	studentA, studentB, studentC := students[0], students[1], students[2]
	past := func(sessionIndex int, student seedStudent) time.Time {
		return attendanceSessions[sessionIndex].startedAt.Add(time.Duration(5+sessionIndex) * time.Minute)
	}
	// No position is fabricated for an uncalibrated venue.
	addWeb := func(sessionIndex int, student seedStudent, status string, onRoster bool) {
		plan.webCheckins = append(plan.webCheckins, seedWebCheckin{
			sessionID: attendanceSessions[sessionIndex].id, studentID: student.id,
			status: status, at: past(sessionIndex, student), onRoster: onRoster,
		})
	}
	addScan := func(sessionIndex int, student seedStudent) {
		deviceID := venueByID(plan.venues, attendanceSessions[sessionIndex].venueID).device
		plan.fingerprintScans = append(plan.fingerprintScans, seedFingerprint{
			sessionID: attendanceSessions[sessionIndex].id, studentID: student.id,
			deviceID: deviceID, at: past(sessionIndex, student).Add(20 * time.Second),
		})
	}
	registered := make(map[string]bool)
	for _, registration := range plan.registrations {
		registered[registration.offeringID+":"+registration.studentID] = true
	}
	for _, student := range []seedStudent{studentA, studentB, studentC} {
		key := plan.offerings[0].id + ":" + student.id
		if !registered[key] {
			plan.registrations = append(plan.registrations, seedRegistration{
				offeringID: plan.offerings[0].id, studentID: student.id, status: "approved",
			})
		}
	}
	addWeb(0, studentA, "present", true)
	addScan(0, studentA)
	addWeb(0, studentB, "present", true)
	addScan(0, studentB)
	addWeb(1, studentB, "present", true)
	addWeb(1, studentC, "present", true)
	addWeb(2, studentA, "present", true)
	addScan(2, studentA)
	addScan(2, studentC)
	addWeb(3, studentB, "present", true)
	addScan(3, studentB)
	addWeb(4, studentA, "late", true)
	addScan(4, studentA)
	addWeb(4, studentB, "present", true)
	plan.overrides = append(plan.overrides, seedOverride{
		sessionID: attendanceSessions[4].id, studentID: studentB.id, status: "excused",
		reason: "Medical appointment", code: "medical", note: "Approved medical absence",
		actorID: plan.users[1].id, at: past(4, studentB),
	})
	addWeb(5, studentB, "present", true)
	plan.overrides = append(plan.overrides, seedOverride{
		sessionID: attendanceSessions[5].id, studentID: studentC.id, status: "absent",
		reason: "No attendance evidence", code: "other", note: "Marked absent for demo reporting",
		actorID: plan.users[1].id, at: attendanceSessions[5].startedAt.Add(60 * time.Minute),
	})
	for _, session := range attendanceSessions {
		deviceID := venueByID(plan.venues, session.venueID).device
		plan.heartbeats = append(plan.heartbeats, seedHeartbeat{
			deviceID: deviceID, at: session.startedAt.Add(5 * time.Minute),
		})
	}
	for _, session := range plan.sessions[6:] {
		if session.venueID == plan.venues[0].id {
			plan.heartbeats = append(plan.heartbeats, seedHeartbeat{deviceID: plan.venues[0].device, at: plan.now})
		}
	}
	// Give the most recent closed session its own report data.
	recent := plan.sessions[7]
	recentAt := recent.startedAt.Add(6 * time.Minute)
	if recent.endedAt != nil && !recentAt.Before(*recent.endedAt) {
		recentAt = recent.startedAt
	}
	plan.webCheckins = append(plan.webCheckins, seedWebCheckin{
		sessionID: recent.id, studentID: studentA.id, status: "present",
		at: recentAt, onRoster: true,
	})
	// The final history fixture has no geofence, so this check-in has no
	// position or distance.
	session := plan.sessions[len(plan.sessions)-1]
	offering := offeringByID(plan.offerings, session.offeringID)
	for _, registration := range plan.registrations {
		if registration.offeringID != offering.id || registration.status != "approved" {
			continue
		}
		candidate := plan.students[studentIndex(plan.students, registration.studentID)]
		plan.webCheckins = append(plan.webCheckins, seedWebCheckin{
			sessionID: session.id, studentID: candidate.id, status: "present",
			at: session.startedAt.Add(4 * time.Minute), onRoster: true,
		})
		break
	}
}

func seedDemoData(ctx context.Context, tx pgx.Tx, plan seedPlan, hashes, deviceHashes map[string][]byte, invitationHash []byte) (map[string]bool, bool, error) {
	now := plan.now
	if err := execSeed(ctx, tx, "institution", `
INSERT INTO institutions(id,name) VALUES($1,'Northbridge Campus Institute')
ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name`, institutionID); err != nil {
		return nil, false, err
	}
	for _, faculty := range plan.faculties {
		if err := execSeed(ctx, tx, "faculty "+faculty.name, `
INSERT INTO faculties(id,institution_id,name) VALUES($1,$2,$3)
ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name`, faculty.id, institutionID, faculty.name); err != nil {
			return nil, false, err
		}
	}
	for _, department := range plan.departments {
		if err := execSeed(ctx, tx, "department "+department.code, `
INSERT INTO departments(id,institution_id,faculty_id,name,code) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(id) DO UPDATE SET faculty_id=EXCLUDED.faculty_id,name=EXCLUDED.name,code=EXCLUDED.code`,
			department.id, institutionID, department.facultyID, department.name, department.code); err != nil {
			return nil, false, err
		}
	}
	for _, programme := range plan.programmes {
		if err := execSeed(ctx, tx, "programme "+programme.code, `
INSERT INTO programmes(id,institution_id,department_id,name,code) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,name=EXCLUDED.name,code=EXCLUDED.code`,
			programme.id, institutionID, programme.departmentID, programme.name, programme.code); err != nil {
			return nil, false, err
		}
	}
	if err := execSeed(ctx, tx, "clear current academic session",
		`UPDATE academic_sessions SET current=false WHERE institution_id=$1`, institutionID); err != nil {
		return nil, false, err
	}
	if err := execSeed(ctx, tx, "academic session", `
INSERT INTO academic_sessions(id,institution_id,name,starts_on,ends_on,current)
VALUES($1,$2,$3,$4::date,$5::date,true)
ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,starts_on=EXCLUDED.starts_on,
ends_on=EXCLUDED.ends_on,current=true`,
		seedID(12), institutionID, fmt.Sprintf("%d-%d", plan.academicStart.Year(), plan.academicStart.Year()+1),
		plan.academicStart.Format("2006-01-02"), plan.academicEnd.Format("2006-01-02")); err != nil {
		return nil, false, err
	}
	if err := execSeed(ctx, tx, "prior semester", `
INSERT INTO semesters(id,institution_id,academic_session_id,name,starts_on,ends_on)
VALUES($1,$2,$3,'Semester 1',$4::date,$5::date)
ON CONFLICT(id) DO UPDATE SET academic_session_id=EXCLUDED.academic_session_id,
name=EXCLUDED.name,starts_on=EXCLUDED.starts_on,ends_on=EXCLUDED.ends_on`,
		seedID(14), institutionID, seedID(12),
		plan.priorSemesterStart.Format("2006-01-02"), plan.priorSemesterEnd.Format("2006-01-02")); err != nil {
		return nil, false, err
	}
	if err := execSeed(ctx, tx, "current semester", `
INSERT INTO semesters(id,institution_id,academic_session_id,name,starts_on,ends_on)
VALUES($1,$2,$3,'Semester 2',$4::date,$5::date)
ON CONFLICT(id) DO UPDATE SET academic_session_id=EXCLUDED.academic_session_id,
name=EXCLUDED.name,starts_on=EXCLUDED.starts_on,ends_on=EXCLUDED.ends_on`,
		seedID(13), institutionID, seedID(12),
		plan.semesterStart.Format("2006-01-02"), plan.semesterEnd.Format("2006-01-02")); err != nil {
		return nil, false, err
	}
	for _, user := range plan.users {
		if err := execSeed(ctx, tx, "user "+user.email, `
INSERT INTO users(id,institution_id,email,password_hash,display_name,email_verified_at,disabled_at)
VALUES($1,$2,$3,$4,$5,$6,NULL)
ON CONFLICT(id) DO UPDATE SET email=EXCLUDED.email,password_hash=EXCLUDED.password_hash,
display_name=EXCLUDED.display_name,email_verified_at=EXCLUDED.email_verified_at,disabled_at=NULL`,
			user.id, institutionID, user.email, hashes[user.id], user.label, now); err != nil {
			return nil, false, err
		}
		if user.role == "lecturer" {
			if err := execSeed(ctx, tx, "staff record "+user.staffNo, `
INSERT INTO staff_records(institution_id,department_id,staff_no,email,display_name,user_id)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(institution_id,staff_no) DO UPDATE SET department_id=EXCLUDED.department_id,
email=EXCLUDED.email,display_name=EXCLUDED.display_name,user_id=EXCLUDED.user_id`,
				institutionID, user.deptID, user.staffNo, user.email, user.label, user.id); err != nil {
				return nil, false, err
			}
		}
	}
	// Release any demo claim the plan no longer intends before re-claiming.
	// student_records is UNIQUE (institution_id, claimed_by_user_id), so when
	// the dataset changes shape - more students, a different split of claimed
	// records - a record still holding last run's user blocks the user's new
	// record. Without this, rerunning the seed over an existing demo database
	// fails instead of converging on the plan.
	recordIDs := make([]string, 0, len(plan.students))
	claimedUserIDs := make([]string, 0, len(plan.students))
	for _, student := range plan.students {
		if student.userID == "" {
			continue
		}
		recordIDs = append(recordIDs, student.id)
		claimedUserIDs = append(claimedUserIDs, student.userID)
	}
	if err := execSeed(ctx, tx, "release stale student claims", `
UPDATE student_records AS sr SET claimed_by_user_id=NULL
WHERE sr.institution_id=$1 AND sr.claimed_by_user_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM unnest($2::uuid[],$3::uuid[]) AS want(record_id,user_id)
    WHERE want.record_id=sr.id AND want.user_id=sr.claimed_by_user_id
  )`, institutionID, recordIDs, claimedUserIDs); err != nil {
		return nil, false, err
	}
	for _, student := range plan.students {
		var claimedBy any
		if student.userID != "" {
			claimedBy = student.userID
		}
		if err := execSeed(ctx, tx, "student record "+student.matric, `
INSERT INTO student_records(id,institution_id,department_id,programme_id,matric_no,email,display_name,level,claimed_by_user_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,programme_id=EXCLUDED.programme_id,
matric_no=EXCLUDED.matric_no,email=EXCLUDED.email,display_name=EXCLUDED.display_name,
level=EXCLUDED.level,claimed_by_user_id=EXCLUDED.claimed_by_user_id`,
			student.id, institutionID, student.departmentID, student.programmeID, student.matric,
			student.email, student.name, student.level, claimedBy); err != nil {
			return nil, false, err
		}
		if student.userID != "" {
			if err := execSeed(ctx, tx, "student role "+student.matric, `
INSERT INTO role_assignments(institution_id,user_id,role,scope_type,department_id)
VALUES($1,$2,'student','department',$3) ON CONFLICT DO NOTHING`,
				institutionID, student.userID, student.departmentID); err != nil {
				return nil, false, err
			}
		}
	}
	for _, user := range plan.users {
		var faculty, department any
		if user.facultyID != "" {
			faculty = user.facultyID
		}
		if user.deptID != "" {
			department = user.deptID
		}
		if err := execSeed(ctx, tx, "role "+user.role+" for "+user.email, `
INSERT INTO role_assignments(institution_id,user_id,role,scope_type,faculty_id,department_id)
VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
			institutionID, user.id, user.role, user.scopeType, faculty, department); err != nil {
			return nil, false, err
		}
	}
	for _, course := range plan.courses {
		if err := execSeed(ctx, tx, "course "+course.code, `
INSERT INTO courses(id,institution_id,department_id,code,title,kind,description,credits)
VALUES($1,$2,$3,$4,$5,$6,$7,3)
ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,code=EXCLUDED.code,
title=EXCLUDED.title,kind=EXCLUDED.kind,description=EXCLUDED.description,credits=EXCLUDED.credits`,
			course.id, institutionID, course.departmentID, course.code, course.title, course.kind, course.description); err != nil {
			return nil, false, err
		}
	}
	for _, offering := range plan.offerings {
		if err := execSeed(ctx, tx, "offering "+offering.id, `
INSERT INTO course_offerings(id,institution_id,course_id,semester_id,coordinator_user_id,level,
registration_mode,roster_policy,min_attendance_pct,late_counts_as_attended)
VALUES($1,$2,$3,$4,$5,$6,$7,'registered_only',$8,$9)
ON CONFLICT(id) DO UPDATE SET course_id=EXCLUDED.course_id,semester_id=EXCLUDED.semester_id,
coordinator_user_id=EXCLUDED.coordinator_user_id,level=EXCLUDED.level,
registration_mode=EXCLUDED.registration_mode,min_attendance_pct=EXCLUDED.min_attendance_pct,
late_counts_as_attended=EXCLUDED.late_counts_as_attended`,
			offering.id, institutionID, offering.courseID, seedID(13), offering.lecturerID,
			offering.level, offering.mode, offering.minimumPercent, offering.lateCounts); err != nil {
			return nil, false, err
		}
		if err := execSeed(ctx, tx, "replace lecturers for offering "+offering.id, `
DELETE FROM offering_lecturers
WHERE institution_id=$1 AND offering_id=$2 AND lecturer_user_id<>$3`,
			institutionID, offering.id, offering.lecturerID); err != nil {
			return nil, false, err
		}
		if err := execSeed(ctx, tx, "offering lecturer "+offering.id, `
INSERT INTO offering_lecturers(institution_id,offering_id,lecturer_user_id)
VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, institutionID, offering.id, offering.lecturerID); err != nil {
			return nil, false, err
		}
		for _, departmentID := range offering.autoTargets {
			if err := execSeed(ctx, tx, "auto target "+offering.id, `
INSERT INTO offering_auto_targets(institution_id,offering_id,department_id,level)
VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, institutionID, offering.id, departmentID, offering.level); err != nil {
				return nil, false, err
			}
		}
	}
	for _, venue := range plan.venues {
		latitude, longitude, radius := venueLocationArgs(venue)
		if err := execSeed(ctx, tx, "venue "+venue.name, `
INSERT INTO venues(id,institution_id,name,latitude,longitude,radius_meters)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,latitude=EXCLUDED.latitude,
longitude=EXCLUDED.longitude,radius_meters=EXCLUDED.radius_meters`,
			venue.id, institutionID, venue.name, latitude, longitude, radius); err != nil {
			return nil, false, err
		}
	}
	for _, timetable := range plan.timetables {
		if err := execSeed(ctx, tx, "timetable "+timetable.id, `
INSERT INTO timetable(id,institution_id,offering_id,venue_id,weekday,starts_at,ends_at)
VALUES($1,$2,$3,$4,$5,$6::time,$7::time)
ON CONFLICT(institution_id,id) DO UPDATE SET offering_id=EXCLUDED.offering_id,
venue_id=EXCLUDED.venue_id,weekday=EXCLUDED.weekday,starts_at=EXCLUDED.starts_at,ends_at=EXCLUDED.ends_at`,
			timetable.id, institutionID, timetable.offeringID, timetable.venueID,
			timetable.weekday, timetable.startsAt, timetable.endsAt); err != nil {
			return nil, false, err
		}
	}
	for _, registration := range plan.registrations {
		var approvedAt any
		if registration.status == "approved" {
			approvedAt = now
		}
		if err := execSeed(ctx, tx, "registration "+registration.studentID, `
INSERT INTO course_registrations(institution_id,offering_id,student_record_id,status,approved_at)
VALUES($1,$2,$3,$4,$5)
ON CONFLICT(institution_id,offering_id,student_record_id) DO UPDATE SET
status=EXCLUDED.status,approved_at=EXCLUDED.approved_at`,
			institutionID, registration.offeringID, registration.studentID, registration.status, approvedAt); err != nil {
			return nil, false, err
		}
	}
	if err := execSeed(ctx, tx, "close legacy open demo session", closeLegacyOpenSessionQuery,
		institutionID, legacyOpenSessionID, now); err != nil {
		return nil, false, err
	}
	if err := execSeed(ctx, tx, "close open demo sessions", closeOpenDemoSessionsQuery,
		institutionID, now); err != nil {
		return nil, false, err
	}
	for _, session := range plan.sessions {
		sessionLatitude, sessionLongitude, sessionRadius := sessionLocationArgs(session)
		if err := execSeed(ctx, tx, "lecture session "+session.id, `
INSERT INTO lecture_sessions(id,institution_id,offering_id,venue_id,timetable_id,started_by_user_id,
started_at,ended_at,latitude,longitude,radius_meters,rotating_code,static_code,code_expires_at,late_after_min)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,NULL,NULL,$12)
ON CONFLICT(id) DO UPDATE SET offering_id=EXCLUDED.offering_id,venue_id=EXCLUDED.venue_id,
timetable_id=EXCLUDED.timetable_id,started_by_user_id=EXCLUDED.started_by_user_id,
started_at=EXCLUDED.started_at,ended_at=EXCLUDED.ended_at,latitude=EXCLUDED.latitude,
longitude=EXCLUDED.longitude,radius_meters=EXCLUDED.radius_meters,rotating_code=true,
static_code=NULL,code_expires_at=NULL,late_after_min=EXCLUDED.late_after_min`,
			session.id, institutionID, session.offeringID, session.venueID, session.timetable,
			session.lecturerID, session.startedAt, session.endedAt, sessionLatitude,
			sessionLongitude, sessionRadius, session.lateAfter); err != nil {
			return nil, false, err
		}
	}
	createdDevices := make(map[string]bool, len(plan.venues))
	for _, venue := range plan.venues {
		var created string
		err := tx.QueryRow(ctx, `
INSERT INTO devices(id,institution_id,venue_id,name,key_hash)
VALUES($1,$2,$3,$4,$5)
ON CONFLICT(institution_id,id) DO NOTHING
RETURNING id::text`, venue.device, institutionID, venue.id,
			venue.name+" Scanner", deviceHashes[venue.device]).Scan(&created)
		if err == nil {
			createdDevices[venue.device] = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("seed scanner at %s: %w", venue.name, err)
		}
	}
	for venueIndex, venue := range plan.venues {
		for studentIndex, student := range plan.students {
			slot := seedDeviceSlot{
				id: seedID(1000 + venueIndex*100 + studentIndex + 1), deviceID: venue.device,
				studentID: student.id, number: studentIndex + 1,
			}
			if err := execSeed(ctx, tx, "device slot "+slot.id, `
INSERT INTO device_slots(id,institution_id,device_id,student_record_id,slot_number)
VALUES($1,$2,$3,$4,$5)
ON CONFLICT(institution_id,device_id,slot_number) DO UPDATE SET
id=EXCLUDED.id,student_record_id=EXCLUDED.student_record_id`,
				slot.id, institutionID, slot.deviceID, slot.studentID, slot.number); err != nil {
				return nil, false, err
			}
		}
	}
	if err := seedAttendanceRows(ctx, tx, plan); err != nil {
		return nil, false, err
	}
	invitationCreated, err := seedWorkflowRows(ctx, tx, plan, invitationHash)
	if err != nil {
		return nil, false, err
	}
	return createdDevices, invitationCreated, nil
}

func seedAttendanceRows(ctx context.Context, tx pgx.Tx, plan seedPlan) error {
	deviceIDs := make([]string, 0, len(plan.venues))
	for _, venue := range plan.venues {
		deviceIDs = append(deviceIDs, venue.device)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM device_heartbeats WHERE institution_id=$1 AND device_id=ANY($2::uuid[])`,
		institutionID, deviceIDs); err != nil {
		return fmt.Errorf("clear demo device heartbeats: %w", err)
	}
	for _, heartbeat := range plan.heartbeats {
		if err := execSeed(ctx, tx, "device heartbeat", `
INSERT INTO device_heartbeats(institution_id,device_id,heartbeat_at) VALUES($1,$2,$3)`,
			institutionID, heartbeat.deviceID, heartbeat.at); err != nil {
			return err
		}
	}
	for _, checkin := range plan.webCheckins {
		var latitude, longitude, accuracy, distance any
		if checkin.position != nil {
			latitude, longitude = checkin.position.latitude, checkin.position.longitude
			accuracy, distance = checkin.position.accuracy, checkin.position.distance
		}
		// Every seeded check-in carries a device hash. Device binding, not
		// distance, is what now stops one handset checking in a row of
		// friends, so a NULL hash would seed data the API would refuse to
		// create.
		if err := execSeed(ctx, tx, "web check-in "+checkin.sessionID, `
INSERT INTO web_checkins(institution_id,session_id,student_record_id,latitude,longitude,
accuracy_meters,distance_meters,device_hash,on_roster,status,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT(session_id,student_record_id) DO UPDATE SET latitude=EXCLUDED.latitude,
longitude=EXCLUDED.longitude,accuracy_meters=EXCLUDED.accuracy_meters,
distance_meters=EXCLUDED.distance_meters,device_hash=EXCLUDED.device_hash,
on_roster=EXCLUDED.on_roster,status=EXCLUDED.status,
created_at=EXCLUDED.created_at`,
			institutionID, checkin.sessionID, checkin.studentID, latitude, longitude,
			accuracy, distance, studentDeviceHash(checkin.studentID),
			checkin.onRoster, checkin.status, checkin.at); err != nil {
			return err
		}
	}
	for _, scan := range plan.fingerprintScans {
		if err := execSeed(ctx, tx, "fingerprint scan "+scan.sessionID, `
INSERT INTO fingerprint_scans(institution_id,session_id,student_record_id,device_id,confidence,client_scanned_at,created_at)
VALUES($1,$2,$3,$4,0.9900,$5,$5)
ON CONFLICT(session_id,student_record_id) DO UPDATE SET device_id=EXCLUDED.device_id,
confidence=EXCLUDED.confidence,client_scanned_at=EXCLUDED.client_scanned_at,created_at=EXCLUDED.created_at`,
			institutionID, scan.sessionID, scan.studentID, scan.deviceID, scan.at); err != nil {
			return err
		}
	}
	for _, override := range plan.overrides {
		if err := execSeed(ctx, tx, "attendance override "+override.sessionID, `
INSERT INTO attendance_overrides(institution_id,session_id,student_record_id,status,reason,
reason_code,note,changed_by_user_id,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT(session_id,student_record_id) DO UPDATE SET status=EXCLUDED.status,reason=EXCLUDED.reason,
reason_code=EXCLUDED.reason_code,note=EXCLUDED.note,changed_by_user_id=EXCLUDED.changed_by_user_id,
created_at=EXCLUDED.created_at`,
			institutionID, override.sessionID, override.studentID, override.status, override.reason,
			override.code, override.note, override.actorID, override.at); err != nil {
			return err
		}
	}
	for _, session := range plan.sessions {
		if session.endedAt == nil {
			continue
		}
		if err := execSeed(ctx, tx, "session audit "+session.id, `
INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details,created_at)
SELECT $1,$2,'session_ended','lecture_session',$3,'{"seeded":true}'::jsonb,$4
WHERE NOT EXISTS (
  SELECT 1 FROM audit_log WHERE institution_id=$1 AND resource_type='lecture_session'
    AND resource_id=$3 AND action='session_ended' AND details->>'seeded'='true'
)`, institutionID, session.lecturerID, session.id, *session.endedAt); err != nil {
			return err
		}
	}
	return nil
}

func seedWorkflowRows(ctx context.Context, tx pgx.Tx, plan seedPlan, invitationHash []byte) (bool, error) {
	var insertedInvitation string
	err := tx.QueryRow(ctx, `
INSERT INTO invitations(id,institution_id,email,role,scope_type,faculty_id,department_id,token_hash,expires_at,accepted_at)
VALUES($1,$2,'new-lecturer@attendance.test','lecturer','department',NULL,$3,$4,$5,NULL)
ON CONFLICT(id) DO NOTHING
RETURNING id::text`, seedID(800), institutionID, plan.departments[0].id,
		invitationHash, plan.now.Add(7*24*time.Hour)).Scan(&insertedInvitation)
	invitationCreated := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("seed pending invitation: %w", err)
	}
	if !invitationCreated {
		if err := execSeed(ctx, tx, "refresh pending invitation expiry", `
UPDATE invitations SET expires_at=$2 WHERE institution_id=$1 AND id=$3 AND accepted_at IS NULL`,
			institutionID, plan.now.Add(7*24*time.Hour), seedID(800)); err != nil {
			return false, err
		}
	}
	if err := execSeed(ctx, tx, "pending signup request", `
INSERT INTO signup_requests(id,institution_id,matric_no,email,status,created_at,department_id)
VALUES($1,$2,'ECE-100-99','pending-signup@attendance.test','pending',$3,$4)
ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id
WHERE signup_requests.status='pending'`,
		seedID(801), institutionID, plan.now, plan.departments[0].id); err != nil {
		return false, err
	}
	for venueIndex, venue := range plan.venues {
		student := firstStudent(plan.students)
		slotNumber := studentIndex(plan.students, student.id) + 1
		slot := seedID(1000 + venueIndex*100 + slotNumber)
		if err := execSeed(ctx, tx, "pending device enrolment "+venue.name, `
INSERT INTO enrolment_tasks(id,institution_id,device_id,student_record_id,slot_id,status,created_at,completed_at)
VALUES($1,$2,$3,$4,$5,'pending',$6,NULL)
ON CONFLICT(id) DO NOTHING`,
			seedID(810+venueIndex), institutionID, venue.device, student.id, slot, plan.now); err != nil {
			return false, err
		}
	}
	for _, session := range plan.sessions {
		if session.endedAt == nil && session.venueID == plan.venues[1].id {
			note := "Scanner offline for maintenance"
			if err := execSeed(ctx, tx, "scanner unavailable status", `
INSERT INTO lecture_session_scanner_status(institution_id,session_id,state,note,changed_by_user_id,created_at)
VALUES($1,$2,'unavailable',$3,$4,$5)
ON CONFLICT(institution_id,session_id) DO UPDATE SET state=EXCLUDED.state,note=EXCLUDED.note,
changed_by_user_id=EXCLUDED.changed_by_user_id,created_at=EXCLUDED.created_at`,
				institutionID, session.id, note, session.lecturerID, plan.now); err != nil {
				return false, err
			}
			if err := execSeed(ctx, tx, "scanner unavailable audit", `
INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details,created_at)
SELECT $1,$2,'scanner_unavailable','lecture_session',$3,
jsonb_build_object('seeded',true,'note',$4::text),$5
WHERE NOT EXISTS (
  SELECT 1 FROM audit_log WHERE institution_id=$1 AND resource_type='lecture_session'
    AND resource_id=$3 AND action='scanner_unavailable' AND details->>'seeded'='true'
)`, institutionID, session.lecturerID, session.id, note, plan.now); err != nil {
				return false, err
			}
		}
	}
	return invitationCreated, nil
}

func resetDemoInstitution(ctx context.Context, tx pgx.Tx) error {
	tables := []string{
		"audit_log", "email_outbox", "disputes", "enrolment_tasks", "attendance_overrides",
		"lecture_session_scanner_status", "device_heartbeats", "fingerprint_scans", "web_checkins",
		"lecture_sessions", "device_slots", "devices", "timetable", "course_registrations",
		"offering_auto_targets", "offering_lecturers", "course_offerings", "courses", "semesters",
		"academic_sessions", "invitations", "signup_requests", "password_resets", "email_verifications",
		"refresh_tokens", "role_assignments", "staff_records", "student_records", "users",
		"programmes", "departments", "faculties",
	}
	for _, table := range tables {
		query := "DELETE FROM " + table + " WHERE institution_id=$1"
		if _, err := tx.Exec(ctx, query, institutionID); err != nil {
			return fmt.Errorf("reset demo table %s: %w", table, err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, institutionID); err != nil {
		return fmt.Errorf("reset demo institution: %w", err)
	}
	return nil
}

func execSeed(ctx context.Context, tx pgx.Tx, label, query string, args ...any) error {
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("seed %s: %w", label, err)
	}
	return nil
}

func (plan seedPlan) validate() error {
	if len(plan.faculties) != 2 || len(plan.departments) != 4 || len(plan.programmes) != 4 ||
		len(plan.courses) < 55 || len(plan.courses) > 70 || len(plan.offerings) != len(plan.courses) ||
		len(plan.students) < 55 || len(plan.students) > 70 || len(plan.venues) != 4 {
		return errors.New("demo seed plan has an invalid organization, course, offering, student, or venue count")
	}
	codes := make(map[string]bool, len(plan.courses))
	coursesByID := make(map[string]seedCourse, len(plan.courses))
	general := 0
	for _, course := range plan.courses {
		if codes[course.code] {
			return fmt.Errorf("duplicate demo course code %q", course.code)
		}
		codes[course.code] = true
		coursesByID[course.id] = course
		if course.kind == "general" {
			general++
		}
	}
	if general < 20 {
		return fmt.Errorf("demo seed plan has %d general courses, want at least 20", general)
	}
	levels := make(map[int]bool, 5)
	lecturerDepartments := make(map[string]string)
	for _, user := range plan.users {
		if user.role == "lecturer" {
			lecturerDepartments[user.id] = user.deptID
		}
	}
	assignedLecturers := make(map[string]bool, len(lecturerDepartments))
	offeringsByID := make(map[string]seedOffering, len(plan.offerings))
	for _, offering := range plan.offerings {
		if offering.level < 100 || offering.level > 500 || offering.level%100 != 0 {
			return fmt.Errorf("demo offering %s has an invalid level %d", offering.id, offering.level)
		}
		course, ok := coursesByID[offering.courseID]
		if !ok {
			return fmt.Errorf("demo offering %s references missing course %s", offering.id, offering.courseID)
		}
		lecturerDepartment, ok := lecturerDepartments[offering.lecturerID]
		if !ok {
			return fmt.Errorf("demo offering %s references missing lecturer %s", offering.id, offering.lecturerID)
		}
		if lecturerDepartment != course.departmentID {
			return fmt.Errorf("demo offering %s lecturer %s is outside course department %s",
				offering.id, offering.lecturerID, course.departmentID)
		}
		assignedLecturers[offering.lecturerID] = true
		offeringsByID[offering.id] = offering
		levels[offering.level] = true
	}
	for lecturerID := range lecturerDepartments {
		if !assignedLecturers[lecturerID] {
			return fmt.Errorf("demo lecturer %s has no offering assignment", lecturerID)
		}
	}
	for _, level := range []int{200, 300, 400, 500} {
		if !levels[level] {
			return fmt.Errorf("demo seed plan has no offering at %d level", level)
		}
	}
	modes := make(map[string]bool)
	for _, offering := range plan.offerings {
		modes[offering.mode] = true
		if offering.lecturerID == "" {
			return errors.New("every demo offering must have an assigned lecturer")
		}
	}
	for _, mode := range []string{"auto", "open", "approval", "closed"} {
		if !modes[mode] {
			return fmt.Errorf("demo seed plan is missing the %s registration mode", mode)
		}
	}
	if len(plan.users) < 15 || len(plan.sessions) < 8 {
		return errors.New("demo seed plan must include role accounts and live/historical sessions")
	}
	claimed, unclaimed := 0, 0
	for _, student := range plan.students {
		if student.userID == "" {
			unclaimed++
			continue
		}
		claimed++
	}
	if claimed < 8 || unclaimed < 20 {
		return errors.New("demo seed plan must include claimed and unclaimed student records")
	}
	// No open classes. Starting one is the lecturer's job, and it is also what
	// supplies the venue's coordinates, so the seed must not pre-empt it or
	// occupy the venue under the one-open-session-per-venue rule.
	for _, session := range plan.sessions {
		if session.endedAt == nil {
			return fmt.Errorf("demo session %s is still open; the seed must leave no live classes", session.id)
		}
		if session.endedAt.Before(session.startedAt) {
			return fmt.Errorf("demo session %s ends before it starts", session.id)
		}
	}
	venueIDs := make(map[string]bool, len(plan.venues))
	deviceIDs := make(map[string]bool, len(plan.venues))
	for _, venue := range plan.venues {
		if venue.located || venue.lat != 0 || venue.lng != 0 || venue.radius != 0 {
			return fmt.Errorf("demo venue %s must have no seeded location or radius", venue.id)
		}
		if venue.id == "" || venueIDs[venue.id] {
			return fmt.Errorf("demo venue has a missing or duplicate ID %q", venue.id)
		}
		if venue.device == "" || deviceIDs[venue.device] {
			return fmt.Errorf("demo venue %s has a missing or duplicate scanner ID %q", venue.id, venue.device)
		}
		venueIDs[venue.id] = true
		deviceIDs[venue.device] = true
	}
	for _, registration := range plan.registrations {
		if registration.status != "approved" && registration.status != "pending" {
			return fmt.Errorf("invalid demo registration status %q", registration.status)
		}
	}
	timetablesByID := make(map[string]seedTimetable, len(plan.timetables))
	offeringsWithTimetable := make(map[string]bool, len(plan.offerings))
	venuesWithTimetable := make(map[string]bool, len(plan.venues))
	for _, timetable := range plan.timetables {
		if _, ok := offeringsByID[timetable.offeringID]; !ok {
			return fmt.Errorf("demo timetable %s references missing offering %s", timetable.id, timetable.offeringID)
		}
		if !venueIDs[timetable.venueID] {
			return fmt.Errorf("demo timetable %s references missing venue %s", timetable.id, timetable.venueID)
		}
		offeringsWithTimetable[timetable.offeringID] = true
		venuesWithTimetable[timetable.venueID] = true
		start, err := time.Parse("15:04:05", timetable.startsAt)
		if err != nil {
			return fmt.Errorf("invalid start time in demo timetable %s: %w", timetable.id, err)
		}
		end, err := time.Parse("15:04:05", timetable.endsAt)
		if err != nil {
			return fmt.Errorf("invalid end time in demo timetable %s: %w", timetable.id, err)
		}
		if !end.After(start) {
			return fmt.Errorf("demo timetable %s ends before it starts", timetable.id)
		}
		timetablesByID[timetable.id] = timetable
	}
	for _, offering := range plan.offerings {
		if !offeringsWithTimetable[offering.id] {
			return fmt.Errorf("demo offering %s has no timetable", offering.id)
		}
	}
	for _, venue := range plan.venues {
		if !venuesWithTimetable[venue.id] {
			return fmt.Errorf("demo venue %s has no timetable", venue.id)
		}
	}
	for _, session := range plan.sessions {
		offering, ok := offeringsByID[session.offeringID]
		if !ok {
			return fmt.Errorf("demo session %s references missing offering %s", session.id, session.offeringID)
		}
		timetable, ok := timetablesByID[session.timetable]
		if !ok || timetable.offeringID != session.offeringID || timetable.venueID != session.venueID {
			return fmt.Errorf("demo session %s does not match its timetable offering and venue", session.id)
		}
		if !venueIDs[session.venueID] {
			return fmt.Errorf("demo session %s references missing venue %s", session.id, session.venueID)
		}
		if session.venueID != plan.venues[0].id {
			return fmt.Errorf("demo session %s must use the primary venue", session.id)
		}
		if session.lecturerID != offering.lecturerID {
			return fmt.Errorf("demo session %s is not started by its assigned lecturer", session.id)
		}
		if session.located || session.latitude != 0 || session.longitude != 0 || session.radius != 0 {
			return fmt.Errorf("demo session %s must have no seeded location or radius", session.id)
		}
	}
	return nil
}

func printCredentials(users []seedUser, students []seedStudent) {
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "Role\tScope\tName\tEmail\tPassword")
	for _, user := range users {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			user.role, user.scopeType, user.label, user.email, user.password)
	}
	_ = writer.Flush()
	fmt.Println("\nUnclaimed student records (matric / email):")
	for _, student := range students {
		if student.userID == "" {
			fmt.Printf("  %s / %s\n", student.matric, student.email)
		}
	}
	fmt.Println("\nInstitution ID:", institutionID)
	fmt.Println("Use the printed institution ID with every login.")
}

func printVenueConfiguration(plan seedPlan) {
	fmt.Println("\nSeed venues have no preset location. The first lecturer to start a class at each venue can capture its location from their device.")
	for _, venue := range plan.venues {
		fmt.Printf("  %s: no location set\n", venue.name)
	}
}

// venueLocationArgs renders a venue's position for SQL, as NULLs when the room
// has never been stood in.
func venueLocationArgs(venue seedVenue) (any, any, any) {
	if !venue.located {
		return nil, nil, nil
	}
	return venue.lat, venue.lng, venue.radius
}

// sessionLocationArgs renders a session's geofence snapshot for SQL.
func sessionLocationArgs(session seedSession) (any, any, any) {
	if !session.located {
		return nil, nil, nil
	}
	return session.latitude, session.longitude, session.radius
}

// studentDeviceHash is a stable per-student stand-in for the hash a browser
// would send. One device per student keeps the seeded rows consistent with the
// one-student-per-device-per-session rule.
func studentDeviceHash(studentID string) []byte {
	digest := sha256.Sum256([]byte("seed-device:" + studentID))
	return digest[:]
}

func seedID(value int) string {
	return fmt.Sprintf("10000000-0000-4000-8000-%012d", value)
}

func studentIndex(students []seedStudent, id string) int {
	for index, student := range students {
		if student.id == id {
			return index
		}
	}
	return -1
}

func departmentIDs(departments []seedDepartment) []string {
	items := make([]string, 0, len(departments))
	for _, department := range departments {
		items = append(items, department.id)
	}
	return items
}

func offeringByID(offerings []seedOffering, id string) seedOffering {
	for _, offering := range offerings {
		if offering.id == id {
			return offering
		}
	}
	return seedOffering{}
}

func offeringIndex(offerings []seedOffering, id string) int {
	for index, offering := range offerings {
		if offering.id == id {
			return index
		}
	}
	return 0
}

func courseByID(courses []seedCourse, id string) seedCourse {
	for _, course := range courses {
		if course.id == id {
			return course
		}
	}
	return seedCourse{}
}

func venueByID(venues []seedVenue, id string) seedVenue {
	for _, venue := range venues {
		if venue.id == id {
			return venue
		}
	}
	return seedVenue{}
}

func sessionByID(sessions []seedSession, id string) seedSession {
	for _, session := range sessions {
		if session.id == id {
			return session
		}
	}
	return seedSession{}
}

func firstStudent(students []seedStudent) seedStudent {
	for _, student := range students {
		if student.userID != "" {
			return student
		}
	}
	return students[0]
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
