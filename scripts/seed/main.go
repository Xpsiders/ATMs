package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"attendance-api/internal/scans"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const institutionID = "10000000-0000-4000-8000-000000000001"

const (
	defaultOriginLatitude  = 6.5244
	defaultOriginLongitude = 3.3792
	defaultVenueRadius     = 150
)

type seedConfig struct {
	originLatitude   float64
	originLongitude  float64
	venueRadius      int
	originConfigured bool
}

type seedPlan struct {
	now              time.Time
	config           seedConfig
	academicStart    time.Time
	academicEnd      time.Time
	semesterStart    time.Time
	semesterEnd      time.Time
	faculties        []seedFaculty
	departments      []seedDepartment
	programmes       []seedProgramme
	users            []seedUser
	students         []seedStudent
	courses          []seedCourse
	offerings        []seedOffering
	venues           []seedVenue
	timetables       []seedTimetable
	sessions         []seedSession
	registrations    []seedRegistration
	webCheckins      []seedWebCheckin
	fingerprintScans []seedFingerprint
	overrides        []seedOverride
	heartbeats       []seedHeartbeat
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
	config, err := loadSeedConfig(os.Getenv)
	if err != nil {
		return err
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
	plan, err := buildSeedPlanWithConfig(now, config)
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
	return buildSeedPlanWithConfig(now, seedConfig{
		originLatitude: defaultOriginLatitude, originLongitude: defaultOriginLongitude,
		venueRadius: defaultVenueRadius,
	})
}

func buildSeedPlanWithConfig(now time.Time, config seedConfig) (seedPlan, error) {
	loc, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return seedPlan{}, fmt.Errorf("load Africa/Lagos timezone: %w", err)
	}
	now = now.UTC()
	localNow := now.In(loc)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	plan := seedPlan{
		now:           now,
		config:        config,
		academicStart: today.AddDate(0, 0, -90),
		academicEnd:   today.AddDate(0, 0, 275),
		semesterStart: today.AddDate(0, 0, -30),
		semesterEnd:   today.AddDate(0, 0, 150),
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
	for index, department := range plan.departments {
		addUser(department.name+" administrator",
			strings.ToLower(department.code)+"-admin@attendance.test",
			"department_admin", "department", "", department.id, "")
		lecturerIDs[index] = addUser(department.name+" lecturer",
			strings.ToLower(department.code)+"-lecturer@attendance.test",
			"lecturer", "department", "", department.id,
			fmt.Sprintf("STAFF-%s-01", department.code))
	}
	studentNumber := 100
	for departmentIndex, department := range plan.departments {
		programmeID := plan.programmes[departmentIndex].id
		for _, level := range []int{100, 200, 300, 400} {
			for ordinal := 0; ordinal < 3; ordinal++ {
				studentID := seedID(studentNumber)
				studentNumber++
				matric := fmt.Sprintf("%s-%d-%02d", department.code, level, ordinal+1)
				email := strings.ToLower(fmt.Sprintf("%s-%d-%02d@student.attendance.test", department.code, level, ordinal+1))
				userID := ""
				if ordinal == 0 {
					userID = addUser(
						fmt.Sprintf("%s %d student %d", department.code, level, ordinal+1),
						email, "student", "department", "", department.id, "")
				}
				plan.students = append(plan.students, seedStudent{
					id: studentID, matric: matric, email: email,
					name:         fmt.Sprintf("%s Student %d (%d)", department.code, ordinal+1, level),
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
	venues, err := placeSeedVenues(config)
	if err != nil {
		return seedPlan{}, err
	}
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
			radius: venue.radius, lateAfter: 10,
		})
		sessionID++
	}
	for index := 0; index < 2; index++ {
		timetable := plan.timetables[index]
		venue := venueByID(venues, timetable.venueID)
		started := currentStart.UTC()
		plan.sessions = append(plan.sessions, seedSession{
			id: seedID(sessionID), offeringID: timetable.offeringID, venueID: timetable.venueID,
			timetable: timetable.id, lecturerID: plan.offerings[offeringIndex(plan.offerings, timetable.offeringID)].lecturerID,
			startedAt: started, latitude: venue.lat, longitude: venue.lng,
			radius: venue.radius, lateAfter: 10,
		})
		sessionID++
	}

	buildRegistrations(&plan)
	buildAttendance(&plan)
	if err := plan.validate(); err != nil {
		return seedPlan{}, err
	}
	return plan, nil
}

func loadSeedConfig(getenv func(string) string) (seedConfig, error) {
	latitudeText := strings.TrimSpace(getenv("SEED_ORIGIN_LAT"))
	longitudeText := strings.TrimSpace(getenv("SEED_ORIGIN_LNG"))
	radiusText := strings.TrimSpace(getenv("SEED_VENUE_RADIUS_M"))
	config := seedConfig{
		originLatitude: defaultOriginLatitude, originLongitude: defaultOriginLongitude,
		venueRadius: defaultVenueRadius,
	}
	if (latitudeText == "") != (longitudeText == "") {
		return seedConfig{}, errors.New("SEED_ORIGIN_LAT and SEED_ORIGIN_LNG must be set together")
	}
	if latitudeText != "" {
		latitude, err := strconv.ParseFloat(latitudeText, 64)
		if err != nil || math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 {
			return seedConfig{}, errors.New("SEED_ORIGIN_LAT must be a number between -90 and 90")
		}
		longitude, err := strconv.ParseFloat(longitudeText, 64)
		if err != nil || math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
			return seedConfig{}, errors.New("SEED_ORIGIN_LNG must be a number between -180 and 180")
		}
		config.originLatitude = latitude
		config.originLongitude = longitude
		config.originConfigured = true
	}
	if radiusText != "" {
		radius, err := strconv.Atoi(radiusText)
		if err != nil || radius < 20 || radius > 2000 {
			return seedConfig{}, errors.New("SEED_VENUE_RADIUS_M must be an integer between 20 and 2000")
		}
		config.venueRadius = radius
	}
	return config, nil
}

func metresToDegreeOffsets(latitude, northMeters, eastMeters float64) (float64, float64, error) {
	if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 ||
		math.IsNaN(northMeters) || math.IsInf(northMeters, 0) ||
		math.IsNaN(eastMeters) || math.IsInf(eastMeters, 0) {
		return 0, 0, errors.New("latitude and metre offsets must be finite and latitude must be between -90 and 90")
	}
	longitudeScale := math.Cos(latitude * math.Pi / 180)
	longitudeOffset := 0.0
	if math.Abs(longitudeScale) >= 1e-9 {
		longitudeOffset = eastMeters / (111320 * longitudeScale)
	}
	return northMeters / 111320, longitudeOffset, nil
}

func placeSeedVenues(config seedConfig) ([]seedVenue, error) {
	if math.IsNaN(config.originLatitude) || math.IsInf(config.originLatitude, 0) ||
		config.originLatitude < -90 || config.originLatitude > 90 ||
		math.IsNaN(config.originLongitude) || math.IsInf(config.originLongitude, 0) ||
		config.originLongitude < -180 || config.originLongitude > 180 ||
		config.venueRadius < 20 || config.venueRadius > 2000 {
		return nil, errors.New("seed origin or venue radius is outside its valid range")
	}
	offsets := []struct {
		id     int
		name   string
		north  float64
		east   float64
		radius int
		device int
	}{
		{id: 400, name: "LT 2", radius: config.venueRadius, device: 410},
		{id: 401, name: "Science Lecture Theatre", north: 20, radius: config.venueRadius, device: 411},
		{id: 402, name: "Innovation Lab", east: 25, radius: config.venueRadius, device: 412},
		{id: 403, name: "Far Test Venue (5 km)", north: 5000, radius: 60, device: 413},
	}
	venues := make([]seedVenue, 0, len(offsets))
	for _, offset := range offsets {
		northMeters := offset.north
		if config.originLatitude+northMeters/111320 > 90 {
			northMeters = -math.Abs(northMeters)
		} else if config.originLatitude+northMeters/111320 < -90 {
			northMeters = math.Abs(northMeters)
		}
		latitudeOffset, longitudeOffset, err := metresToDegreeOffsets(
			config.originLatitude, northMeters, offset.east)
		if err != nil {
			return nil, fmt.Errorf("place venue %s: %w", offset.name, err)
		}
		latitude := config.originLatitude + latitudeOffset
		longitude := config.originLongitude
		if longitudeOffset != 0 {
			longitude = math.Mod(config.originLongitude+longitudeOffset+180, 360)
			if longitude < 0 {
				longitude += 360
			}
			longitude -= 180
		}
		if latitude < -90 || latitude > 90 {
			return nil, fmt.Errorf("origin coordinates cannot place venue %s within valid coordinate ranges", offset.name)
		}
		venues = append(venues, seedVenue{
			id: seedID(offset.id), name: offset.name, lat: latitude, lng: longitude,
			radius: offset.radius, device: seedID(offset.device),
		})
	}
	return venues, nil
}

func updateSeedVenues(ctx context.Context, tx pgx.Tx, plan seedPlan) error {
	for _, venue := range plan.venues {
		if err := execSeed(ctx, tx, "update venue "+venue.name, `
INSERT INTO venues(id,institution_id,name,latitude,longitude,radius_meters)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(institution_id,id) DO UPDATE SET name=EXCLUDED.name,
latitude=EXCLUDED.latitude,longitude=EXCLUDED.longitude,radius_meters=EXCLUDED.radius_meters`,
			venue.id, institutionID, venue.name, venue.lat, venue.lng, venue.radius); err != nil {
			return err
		}
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
	for _, student := range plan.students {
		if student.userID == "" {
			continue
		}
		for _, offering := range plan.offerings {
			course := courseByID(plan.courses, offering.courseID)
			if offering.level != student.level ||
				course.kind == "departmental" && course.departmentID != student.departmentID ||
				course.kind == "general" && !contains(offering.autoTargets, student.departmentID) && offering.mode == "auto" {
				continue
			}
			status := ""
			studentOrdinal := studentIndex(plan.students, student.id)
			switch offering.mode {
			case "auto":
				if course.kind == "departmental" || contains(offering.autoTargets, student.departmentID) {
					status = "approved"
				} else {
					status = "pending"
				}
			case "open":
				if studentOrdinal%2 == 0 {
					status = "approved"
				}
			case "approval":
				if studentOrdinal%4 == 0 {
					status = "approved"
				} else {
					status = "pending"
				}
			}
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
	plan.webCheckins = append(plan.webCheckins, seedWebCheckin{
		sessionID: plan.sessions[7].id, studentID: studentA.id, status: "present",
		at: plan.now, onRoster: true,
	})
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
	if err := execSeed(ctx, tx, "current semester", `
INSERT INTO semesters(id,institution_id,academic_session_id,name,starts_on,ends_on)
VALUES($1,$2,$3,'Semester 1',$4::date,$5::date)
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
		if err := execSeed(ctx, tx, "venue "+venue.name, `
INSERT INTO venues(id,institution_id,name,latitude,longitude,radius_meters)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,latitude=EXCLUDED.latitude,
longitude=EXCLUDED.longitude,radius_meters=EXCLUDED.radius_meters`,
			venue.id, institutionID, venue.name, venue.lat, venue.lng, venue.radius); err != nil {
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
	for _, session := range plan.sessions {
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
			session.lecturerID, session.startedAt, session.endedAt, session.latitude,
			session.longitude, session.radius, session.lateAfter); err != nil {
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
		venue := venueByID(plan.venues, sessionByID(plan.sessions, checkin.sessionID).venueID)
		if err := execSeed(ctx, tx, "web check-in "+checkin.sessionID, `
INSERT INTO web_checkins(institution_id,session_id,student_record_id,latitude,longitude,
accuracy_meters,distance_meters,device_hash,on_roster,status,created_at)
VALUES($1,$2,$3,$4,$5,5,2,NULL,$6,$7,$8)
ON CONFLICT(session_id,student_record_id) DO UPDATE SET latitude=EXCLUDED.latitude,
longitude=EXCLUDED.longitude,accuracy_meters=EXCLUDED.accuracy_meters,
distance_meters=EXCLUDED.distance_meters,on_roster=EXCLUDED.on_roster,status=EXCLUDED.status,
created_at=EXCLUDED.created_at`,
			institutionID, checkin.sessionID, checkin.studentID, venue.lat, venue.lng,
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
INSERT INTO signup_requests(id,institution_id,matric_no,email,status,created_at)
VALUES($1,$2,'ECE-100-99','pending-signup@attendance.test','pending',$3)
ON CONFLICT(id) DO NOTHING`,
		seedID(801), institutionID, plan.now); err != nil {
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
		len(plan.courses) < 8 || len(plan.courses) > 12 || len(plan.offerings) < 10 ||
		len(plan.students) < 30 || len(plan.students) > 50 || len(plan.venues) < 3 {
		return errors.New("demo seed plan has an invalid organization, course, offering, student, or venue count")
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
	openVenues := map[string]bool{}
	for _, session := range plan.sessions {
		if session.endedAt == nil {
			if openVenues[session.venueID] {
				return errors.New("demo seed plan has more than one open session at a venue")
			}
			openVenues[session.venueID] = true
		}
	}
	if len(openVenues) < 2 {
		return errors.New("demo seed plan must include live sessions at two venues")
	}
	for _, registration := range plan.registrations {
		if registration.status != "approved" && registration.status != "pending" {
			return fmt.Errorf("invalid demo registration status %q", registration.status)
		}
	}
	for _, timetable := range plan.timetables {
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
	fmt.Printf("\nSeed venue origin: latitude %.7f, longitude %.7f (radius %d m)\n",
		plan.config.originLatitude, plan.config.originLongitude, plan.config.venueRadius)
	for _, venue := range plan.venues {
		fmt.Printf("  %s: latitude %.7f, longitude %.7f, radius %d m\n",
			venue.name, venue.lat, venue.lng, venue.radius)
	}
	if !plan.config.originConfigured {
		fmt.Println("WARNING: No SEED_ORIGIN_LAT/SEED_ORIGIN_LNG configured. Check-ins from your real location will be rejected with outside_radius until both are set.")
	}
	fmt.Println("Set SEED_ORIGIN_LAT and SEED_ORIGIN_LNG (and optionally SEED_VENUE_RADIUS_M) in .env, then run `go run ./scripts/seed --update-venues` or `docker compose run --rm seed seed --update-venues` to update venues and open-session snapshots.")
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
