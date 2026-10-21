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

const (
	institutionID   = "10000000-0000-4000-8000-000000000001"
	facultyID       = "10000000-0000-4000-8000-000000000002"
	departmentAID   = "10000000-0000-4000-8000-000000000003"
	departmentBID   = "10000000-0000-4000-8000-000000000004"
	programmeAID    = "10000000-0000-4000-8000-000000000005"
	programmeBID    = "10000000-0000-4000-8000-000000000006"
	academicID      = "10000000-0000-4000-8000-000000000007"
	semesterID      = "10000000-0000-4000-8000-000000000008"
	courseID        = "10000000-0000-4000-8000-000000000009"
	offering100ID   = "10000000-0000-4000-8000-000000000010"
	offering200ID   = "10000000-0000-4000-8000-000000000011"
	venueAID        = "10000000-0000-4000-8000-000000000012"
	venueBID        = "10000000-0000-4000-8000-000000000013"
	deviceID        = "10000000-0000-4000-8000-000000000014"
	student100AID   = "10000000-0000-4000-8000-000000000015"
	student200AID   = "10000000-0000-4000-8000-000000000016"
	student100BID   = "10000000-0000-4000-8000-000000000017"
	student200BID   = "10000000-0000-4000-8000-000000000018"
	adminID         = "10000000-0000-4000-8000-000000000019"
	deptAdminAID    = "10000000-0000-4000-8000-000000000020"
	deptAdminBID    = "10000000-0000-4000-8000-000000000021"
	lecturerAID     = "10000000-0000-4000-8000-000000000022"
	lecturerBID     = "10000000-0000-4000-8000-000000000023"
	studentUserID   = "10000000-0000-4000-8000-000000000024"
	facultyAdminID  = "10000000-0000-4000-8000-000000000025"
	platformAdminID = "10000000-0000-4000-8000-000000000026"
	timetable100ID  = "10000000-0000-4000-8000-000000000027"
	timetable200ID  = "10000000-0000-4000-8000-000000000028"
)

type demoFaculty struct {
	id   string
	name string
}

type demoDepartment struct {
	id, facultyID, programmeID, name, code string
}

type demoCourse struct {
	id, departmentID, code, title, kind, description, offeringID, lecturerID, venueID string
	level                                                                             int
	mode                                                                              string
}

type demoVenue struct {
	id, name            string
	latitude, longitude float64
	radius              int
}

type demoStudent struct {
	id, departmentID, programmeID, matric, email, name, userID, password string
	level                                                                int
	claimed                                                              bool
}

type demoCredential struct {
	label, userID, email, password, role, scopeType, facultyID, departmentID string
}

type teachingAssignment struct {
	timetableID, offeringID, venueID string
	weekday                          int
	startsAt, endsAt                 string
}

type attendanceFixture struct {
	student        demoStudent
	webStatus      string
	scan           bool
	overrideStatus string
	overrideReason string
}

var (
	facultyBID      = demoUUID(100)
	departmentCID   = demoUUID(101)
	departmentDID   = demoUUID(102)
	programmeCID    = demoUUID(103)
	programmeDID    = demoUUID(104)
	venueCID        = demoUUID(105)
	lecturerCID     = demoUUID(106)
	lecturerDID     = demoUUID(107)
	facultyAdminBID = demoUUID(108)
	deptAdminCID    = demoUUID(109)
	deptAdminDID    = demoUUID(110)

	demoFaculties = []demoFaculty{
		{id: facultyID, name: "Faculty of Engineering"},
		{id: facultyBID, name: "Faculty of Computing and Business"},
	}
	demoDepartments = []demoDepartment{
		{id: departmentAID, facultyID: facultyID, programmeID: programmeAID, name: "Electrical and Computer Engineering", code: "ECE"},
		{id: departmentBID, facultyID: facultyID, programmeID: programmeBID, name: "Mechanical Engineering", code: "ME"},
		{id: departmentCID, facultyID: facultyBID, programmeID: programmeCID, name: "Computer Science", code: "CSC"},
		{id: departmentDID, facultyID: facultyBID, programmeID: programmeDID, name: "Business Administration", code: "BUS"},
	}
	demoVenues = []demoVenue{
		{id: venueAID, name: "Engineering Hall", latitude: 6.5244, longitude: 3.3792, radius: 150},
		{id: venueBID, name: "Workshop Building", latitude: 6.5251, longitude: 3.3810, radius: 120},
		{id: venueCID, name: "Computing Centre", latitude: 6.5262, longitude: 3.3775, radius: 100},
	}
	demoCourses = []demoCourse{
		{id: courseID, departmentID: departmentAID, code: "ECE301", title: "Circuits and Systems", kind: "general", description: "Cross-department circuit analysis", offeringID: offering100ID, lecturerID: lecturerAID, venueID: venueAID, level: 100, mode: "auto"},
		{id: courseID, departmentID: departmentAID, code: "ECE301", title: "Circuits and Systems", kind: "general", description: "Cross-department circuit analysis", offeringID: offering200ID, lecturerID: lecturerBID, venueID: venueBID, level: 200, mode: "auto"},
		{id: demoUUID(200), departmentID: departmentCID, code: "CSC101", title: "Programming Fundamentals", kind: "departmental", description: "Core programming concepts", offeringID: demoUUID(300), lecturerID: lecturerCID, venueID: venueCID, level: 100, mode: "auto"},
		{id: demoUUID(201), departmentID: departmentBID, code: "MEC120", title: "Engineering Statics", kind: "departmental", description: "Core statics and mechanics", offeringID: demoUUID(301), lecturerID: lecturerBID, venueID: venueBID, level: 100, mode: "open"},
		{id: demoUUID(202), departmentID: departmentDID, code: "BUS110", title: "Business Foundations", kind: "departmental", description: "Core business principles", offeringID: demoUUID(302), lecturerID: lecturerDID, venueID: venueCID, level: 100, mode: "approval"},
		{id: demoUUID(203), departmentID: departmentAID, code: "ECE210", title: "Digital Logic", kind: "departmental", description: "Core digital systems", offeringID: demoUUID(303), lecturerID: lecturerAID, venueID: venueAID, level: 200, mode: "closed"},
		{id: demoUUID(204), departmentID: departmentCID, code: "CSC220", title: "Data Structures", kind: "departmental", description: "Elective algorithms and data structures", offeringID: demoUUID(304), lecturerID: lecturerCID, venueID: venueCID, level: 200, mode: "open"},
		{id: demoUUID(205), departmentID: departmentBID, code: "MEC230", title: "Thermodynamics", kind: "departmental", description: "Core thermodynamic systems", offeringID: demoUUID(305), lecturerID: lecturerBID, venueID: venueBID, level: 200, mode: "approval"},
		{id: demoUUID(206), departmentID: departmentDID, code: "BUS230", title: "Principles of Marketing", kind: "departmental", description: "Elective marketing practice", offeringID: demoUUID(306), lecturerID: lecturerDID, venueID: venueCID, level: 200, mode: "auto"},
		{id: demoUUID(207), departmentID: departmentAID, code: "ECE310", title: "Embedded Systems", kind: "departmental", description: "Elective embedded systems", offeringID: demoUUID(307), lecturerID: lecturerAID, venueID: venueAID, level: 300, mode: "approval"},
		{id: demoUUID(208), departmentID: departmentCID, code: "CSC330", title: "Computer Networks", kind: "departmental", description: "Core network systems", offeringID: demoUUID(308), lecturerID: lecturerCID, venueID: venueCID, level: 300, mode: "closed"},
	}
)

func main() {
	reset := flag.Bool("reset", false, "delete and recreate the demo institution; removes every record in that tenant")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := run(ctx, *reset); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, reset bool) error {
	if err := demoPlan().validate(); err != nil {
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
	credentials := makeDemoCredentials()
	hashes := make(map[string][]byte, len(credentials))
	for _, credential := range credentials {
		hash, err := bcrypt.GenerateFromPassword([]byte(credential.password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash demo password for %s: %w", credential.label, err)
		}
		hashes[credential.userID] = hash
	}
	deviceKey, deviceHash, err := scans.GenerateDeviceKey(nil)
	if err != nil {
		return err
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	if reset {
		if _, err := tx.Exec(ctx, "DELETE FROM institutions WHERE id=$1", institutionID); err != nil {
			return fmt.Errorf("reset demo institution: %w", err)
		}
	}
	deviceCreated, err := seedDemoData(ctx, tx, hashes, deviceHash, now)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit demo data: %w", err)
	}

	printCredentials(credentials)
	printUnclaimedStudents()
	if deviceCreated {
		fmt.Printf("\nRegistered scanner key (shown only on initial creation): %s\n", deviceKey)
	} else {
		fmt.Println("\nThe demo scanner already exists; its key was not rotated or displayed.")
	}
	if reset {
		fmt.Println("Reset complete: only the seeded demo institution was deleted and recreated.")
	}
	return nil
}

func seedDemoData(ctx context.Context, tx pgx.Tx, hashes map[string][]byte, deviceHash []byte, now time.Time) (bool, error) {
	if err := execSeed(ctx, tx, "institution", `
		INSERT INTO institutions(id,name) VALUES($1,$2)
		ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name`, institutionID, "Northbridge Campus Institute"); err != nil {
		return false, err
	}
	for _, faculty := range demoFaculties {
		if err := execSeed(ctx, tx, "faculty "+faculty.name, `
			INSERT INTO faculties(id,institution_id,name) VALUES($1,$2,$3)
			ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name`, faculty.id, institutionID, faculty.name); err != nil {
			return false, err
		}
	}
	for _, department := range demoDepartments {
		if err := execSeed(ctx, tx, "department "+department.code, `
			INSERT INTO departments(id,institution_id,faculty_id,name,code) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT(id) DO UPDATE SET faculty_id=EXCLUDED.faculty_id,name=EXCLUDED.name,code=EXCLUDED.code`,
			department.id, institutionID, department.facultyID, department.name, department.code); err != nil {
			return false, err
		}
		if err := execSeed(ctx, tx, "programme "+department.code, `
			INSERT INTO programmes(id,institution_id,department_id,name,code) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,name=EXCLUDED.name,code=EXCLUDED.code`,
			department.programmeID, institutionID, department.id, department.name+" Programme", department.code+"-BSC"); err != nil {
			return false, err
		}
	}

	academicStart, academicEnd, semesterStart, semesterEnd := academicPeriod(now)
	if _, err := tx.Exec(ctx, `UPDATE academic_sessions SET current=false WHERE institution_id=$1 AND current=true`, institutionID); err != nil {
		return false, fmt.Errorf("clear current academic session: %w", err)
	}
	if err := execSeed(ctx, tx, "current academic session", `
		INSERT INTO academic_sessions(id,institution_id,name,starts_on,ends_on,current)
		VALUES($1,$2,$3,$4,$5,true)
		ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,starts_on=EXCLUDED.starts_on,ends_on=EXCLUDED.ends_on,current=true`,
		academicID, institutionID, fmt.Sprintf("%d-%d", academicStart.Year(), academicEnd.Year()), academicStart, academicEnd); err != nil {
		return false, err
	}
	if err := execSeed(ctx, tx, "current semester", `
		INSERT INTO semesters(id,institution_id,academic_session_id,name,starts_on,ends_on)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(id) DO UPDATE SET academic_session_id=EXCLUDED.academic_session_id,name=EXCLUDED.name,
		starts_on=EXCLUDED.starts_on,ends_on=EXCLUDED.ends_on`,
		semesterID, institutionID, academicID, "Current Semester", semesterStart, semesterEnd); err != nil {
		return false, err
	}

	credentials := makeDemoCredentials()
	for _, credential := range credentials {
		if err := execSeed(ctx, tx, "user "+credential.email, `
			INSERT INTO users(id,institution_id,email,password_hash,display_name,email_verified_at,disabled_at)
			VALUES($1,$2,$3,$4,$5,$6,NULL)
			ON CONFLICT(id) DO UPDATE SET email=EXCLUDED.email,password_hash=EXCLUDED.password_hash,
			display_name=EXCLUDED.display_name,email_verified_at=EXCLUDED.email_verified_at,disabled_at=NULL`,
			credential.userID, institutionID, credential.email, hashes[credential.userID], credential.label, now); err != nil {
			return false, err
		}
	}
	for _, credential := range credentials {
		var faculty, department any
		if credential.facultyID != "" {
			faculty = credential.facultyID
		}
		if credential.departmentID != "" {
			department = credential.departmentID
		}
		if err := execSeed(ctx, tx, "role for "+credential.email, `
			INSERT INTO role_assignments(institution_id,user_id,role,scope_type,faculty_id,department_id)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
			institutionID, credential.userID, credential.role, credential.scopeType, faculty, department); err != nil {
			return false, err
		}
	}

	for _, venue := range demoVenues {
		if err := execSeed(ctx, tx, "venue "+venue.name, `
			INSERT INTO venues(id,institution_id,name,latitude,longitude,radius_meters)
			VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,latitude=EXCLUDED.latitude,
			longitude=EXCLUDED.longitude,radius_meters=EXCLUDED.radius_meters`,
			venue.id, institutionID, venue.name, venue.latitude, venue.longitude, venue.radius); err != nil {
			return false, err
		}
	}
	for _, student := range demoStudents() {
		var claimedBy any
		if student.claimed {
			claimedBy = student.userID
		}
		if err := execSeed(ctx, tx, "student record "+student.matric, `
			INSERT INTO student_records(id,institution_id,department_id,programme_id,matric_no,email,display_name,level,claimed_by_user_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,programme_id=EXCLUDED.programme_id,
			matric_no=EXCLUDED.matric_no,email=EXCLUDED.email,display_name=EXCLUDED.display_name,level=EXCLUDED.level,
			claimed_by_user_id=coalesce(student_records.claimed_by_user_id,EXCLUDED.claimed_by_user_id)`,
			student.id, institutionID, student.departmentID, student.programmeID, student.matric, student.email,
			student.name, student.level, claimedBy); err != nil {
			return false, err
		}
	}
	for _, student := range demoStudents() {
		if !student.claimed {
			continue
		}
		if err := execSeed(ctx, tx, "student role "+student.email, `
			INSERT INTO role_assignments(institution_id,user_id,role,scope_type,department_id)
			VALUES($1,$2,'student','department',$3) ON CONFLICT DO NOTHING`,
			institutionID, student.userID, student.departmentID); err != nil {
			return false, err
		}
	}
	for _, lecturer := range lecturers() {
		if err := execSeed(ctx, tx, "staff record "+lecturer.email, `
			INSERT INTO staff_records(institution_id,department_id,staff_no,email,display_name,user_id)
			VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(institution_id,staff_no) DO UPDATE SET department_id=EXCLUDED.department_id,
			email=EXCLUDED.email,display_name=EXCLUDED.display_name,user_id=EXCLUDED.user_id`,
			institutionID, lecturer.departmentID, lecturer.staffNo, lecturer.email, lecturer.name, lecturer.userID); err != nil {
			return false, err
		}
	}

	if err := seedCourses(ctx, tx); err != nil {
		return false, err
	}
	if err := seedTeachingAndRegistration(ctx, tx, now); err != nil {
		return false, err
	}
	deviceCreated, err := seedDevice(ctx, tx, deviceHash)
	if err != nil {
		return false, err
	}
	if err := seedAttendanceHistory(ctx, tx, now); err != nil {
		return false, err
	}
	if err := seedPendingWork(ctx, tx, now); err != nil {
		return false, err
	}
	return deviceCreated, nil
}

func seedCourses(ctx context.Context, tx pgx.Tx) error {
	seenCourses := map[string]bool{}
	for _, course := range demoCourses {
		if seenCourses[course.id] {
			continue
		}
		seenCourses[course.id] = true
		if err := execSeed(ctx, tx, "course "+course.code, `
			INSERT INTO courses(id,institution_id,department_id,code,title,kind,description,credits)
			VALUES($1,$2,$3,$4,$5,$6,$7,3)
			ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,code=EXCLUDED.code,
			title=EXCLUDED.title,kind=EXCLUDED.kind,description=EXCLUDED.description`,
			course.id, institutionID, course.departmentID, course.code, course.title, course.kind, course.description); err != nil {
			return err
		}
	}
	for _, course := range demoCourses {
		if err := execSeed(ctx, tx, "offering "+course.code, `
			INSERT INTO course_offerings(id,institution_id,course_id,semester_id,coordinator_user_id,level,
			registration_mode,roster_policy,min_attendance_pct,late_counts_as_attended)
			VALUES($1,$2,$3,$4,$5,$6,$7,'registered_only',75,true)
			ON CONFLICT(id) DO UPDATE SET course_id=EXCLUDED.course_id,semester_id=EXCLUDED.semester_id,
			coordinator_user_id=EXCLUDED.coordinator_user_id,level=EXCLUDED.level,
			registration_mode=EXCLUDED.registration_mode,roster_policy=EXCLUDED.roster_policy,
			min_attendance_pct=EXCLUDED.min_attendance_pct,late_counts_as_attended=EXCLUDED.late_counts_as_attended`,
			course.offeringID, institutionID, course.id, semesterID, course.lecturerID, course.level, course.mode); err != nil {
			return err
		}
		if err := execSeed(ctx, tx, "lecturer assignment "+course.code, `
			INSERT INTO offering_lecturers(institution_id,offering_id,lecturer_user_id)
			VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, institutionID, course.offeringID, course.lecturerID); err != nil {
			return err
		}
		if course.kind == "general" {
			for _, department := range demoDepartments {
				if err := execSeed(ctx, tx, "general auto target "+course.code, `
					INSERT INTO offering_auto_targets(institution_id,offering_id,department_id,level)
					VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
					institutionID, course.offeringID, department.id, course.level); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func seedTeachingAndRegistration(ctx context.Context, tx pgx.Tx, now time.Time) error {
	assignments := weeklyAssignments(now)
	for _, assignment := range assignments {
		if err := execSeed(ctx, tx, "weekly timetable", `
			INSERT INTO timetable(id,institution_id,offering_id,venue_id,weekday,starts_at,ends_at)
			VALUES($1,$2,$3,$4,$5,$6::time,$7::time)
			ON CONFLICT(institution_id,id) DO UPDATE SET offering_id=EXCLUDED.offering_id,venue_id=EXCLUDED.venue_id,
			weekday=EXCLUDED.weekday,starts_at=EXCLUDED.starts_at,ends_at=EXCLUDED.ends_at`,
			assignment.timetableID, institutionID, assignment.offeringID, assignment.venueID,
			assignment.weekday, assignment.startsAt, assignment.endsAt); err != nil {
			return err
		}
	}
	for _, student := range demoStudents() {
		for _, course := range demoCourses {
			if student.level != course.level || !eligibleForCourse(student, course) || course.mode == "closed" {
				continue
			}
			status := "approved"
			if course.mode == "approval" {
				status = "pending"
			}
			if err := execSeed(ctx, tx, "registration "+student.matric, `
				INSERT INTO course_registrations(institution_id,offering_id,student_record_id,status,approved_at)
				VALUES($1,$2,$3,$4,CASE WHEN $4='approved' THEN $5 ELSE NULL END)
				ON CONFLICT(institution_id,offering_id,student_record_id) DO UPDATE SET
				status=EXCLUDED.status,approved_at=EXCLUDED.approved_at`,
				institutionID, course.offeringID, student.id, status, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func eligibleForCourse(student demoStudent, course demoCourse) bool {
	if course.kind == "departmental" {
		return course.departmentID == student.departmentID
	}
	return course.kind == "general"
}

func seedDevice(ctx context.Context, tx pgx.Tx, deviceHash []byte) (bool, error) {
	var createdID string
	err := tx.QueryRow(ctx, `
		INSERT INTO devices(id,institution_id,venue_id,name,key_hash)
		VALUES($1,$2,$3,'Engineering Hall Scanner',$4)
		ON CONFLICT(institution_id,id) DO NOTHING
		RETURNING id::text`, deviceID, institutionID, venueAID, deviceHash).Scan(&createdID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := execSeed(ctx, tx, "scanner student slot", `
			INSERT INTO device_slots(institution_id,device_id,student_record_id,slot_number)
			VALUES($1,$2,$3,1) ON CONFLICT(institution_id,device_id,slot_number)
			DO UPDATE SET student_record_id=EXCLUDED.student_record_id`,
			institutionID, deviceID, student100AID); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("seed demo scanner: %w", err)
	}
	if err := execSeed(ctx, tx, "scanner student slot", `
		INSERT INTO device_slots(institution_id,device_id,student_record_id,slot_number)
		VALUES($1,$2,$3,1) ON CONFLICT(institution_id,device_id,slot_number)
		DO UPDATE SET student_record_id=EXCLUDED.student_record_id`,
		institutionID, deviceID, student100AID); err != nil {
		return false, err
	}
	return true, nil
}

func seedAttendanceHistory(ctx context.Context, tx pgx.Tx, now time.Time) error {
	venue := demoVenues[0]
	fixtures := demoAttendanceFixtures()
	for index := 0; index < 8; index++ {
		sessionID := demoUUID(4000 + index)
		startedAt := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, time.UTC).AddDate(0, 0, -(index + 1))
		endedAt := startedAt.Add(time.Hour)
		if err := execSeed(ctx, tx, "past lecture session", `
			INSERT INTO lecture_sessions(id,institution_id,offering_id,venue_id,timetable_id,started_by_user_id,
			started_at,ended_at,latitude,longitude,radius_meters,rotating_code,late_after_min)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,10)
			ON CONFLICT(institution_id,id) DO UPDATE SET started_at=EXCLUDED.started_at,ended_at=EXCLUDED.ended_at`,
			sessionID, institutionID, offering100ID, venue.id, assignmentsTimetableID(0), lecturerAID,
			startedAt, endedAt, venue.latitude, venue.longitude, venue.radius); err != nil {
			return err
		}
		for _, fixture := range fixtures[index] {
			if fixture.webStatus != "" {
				if err := insertWebCheckin(ctx, tx, sessionID, fixture.student, startedAt.Add(15*time.Minute), fixture.webStatus); err != nil {
					return err
				}
			}
			if fixture.scan {
				if err := insertFingerprintScan(ctx, tx, sessionID, fixture.student, startedAt.Add(15*time.Minute)); err != nil {
					return err
				}
			}
			if fixture.overrideStatus != "" {
				if err := insertOverride(ctx, tx, sessionID, fixture.student, fixture.overrideStatus, fixture.overrideReason); err != nil {
					return err
				}
			}
		}
	}
	if err := seedOpenSession(ctx, tx, now); err != nil {
		return err
	}
	return nil
}

func demoAttendanceFixtures() [][]attendanceFixture {
	fixtures := make([][]attendanceFixture, 8)
	lowStudent := demoStudents()[0]
	for index := range fixtures {
		if index < 5 {
			fixtures[index] = append(fixtures[index], attendanceFixture{
				student: lowStudent, webStatus: "present", scan: true,
			})
		} else {
			fixtures[index] = append(fixtures[index], attendanceFixture{
				student: lowStudent, overrideStatus: "absent", overrideReason: "Demo absence for below-threshold report",
			})
		}
	}
	scenarioStudents := make([]demoStudent, 0, 3)
	for _, department := range demoDepartments[1:] {
		for _, student := range demoStudents() {
			if student.departmentID == department.id && student.level == 100 {
				scenarioStudents = append(scenarioStudents, student)
				break
			}
		}
	}
	if len(scenarioStudents) >= 3 {
		fixtures[0] = append(fixtures[0], attendanceFixture{
			student: scenarioStudents[0], webStatus: "present", scan: true,
			overrideStatus: "present", overrideReason: "Reviewed demo attendance",
		})
		fixtures[1] = append(fixtures[1], attendanceFixture{student: scenarioStudents[1], webStatus: "present"})
		fixtures[2] = append(fixtures[2], attendanceFixture{student: scenarioStudents[2], scan: true})
	}
	fixtures[0] = append(fixtures[0], attendanceFixture{
		student: demoStudentInDepartment(departmentAID, 100, lowStudent.id), webStatus: "late", scan: true,
	})
	return fixtures
}

func seedOpenSession(ctx context.Context, tx pgx.Tx, now time.Time) error {
	venue := demoVenues[0]
	startedAt := now.Add(-10 * time.Minute)
	return execSeed(ctx, tx, "open demo session", `
		INSERT INTO lecture_sessions(id,institution_id,offering_id,venue_id,timetable_id,started_by_user_id,
		started_at,ended_at,latitude,longitude,radius_meters,rotating_code,late_after_min)
		VALUES($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9,$10,true,10)
		ON CONFLICT(institution_id,id) DO UPDATE SET started_at=EXCLUDED.started_at,ended_at=NULL`,
		demoUUID(4100), institutionID, offering100ID, venue.id, assignmentsTimetableID(0),
		lecturerAID, startedAt, venue.latitude, venue.longitude, venue.radius)
}

func insertWebCheckin(ctx context.Context, tx pgx.Tx, sessionID string, student demoStudent, checkedAt time.Time, status string) error {
	venue := demoVenues[0]
	return execSeed(ctx, tx, "demo web check-in", `
		INSERT INTO web_checkins(institution_id,session_id,student_record_id,latitude,longitude,accuracy_meters,
		distance_meters,device_hash,on_roster,status,created_at)
		VALUES($1,$2,$3,$4,$5,8,0,NULL,true,$6,$7)
		ON CONFLICT(session_id,student_record_id) DO UPDATE SET status=EXCLUDED.status,created_at=EXCLUDED.created_at`,
		institutionID, sessionID, student.id, venue.latitude, venue.longitude, status, checkedAt)
}

func insertFingerprintScan(ctx context.Context, tx pgx.Tx, sessionID string, student demoStudent, scannedAt time.Time) error {
	return execSeed(ctx, tx, "demo fingerprint scan", `
		INSERT INTO fingerprint_scans(institution_id,session_id,student_record_id,device_id,confidence,client_scanned_at,created_at)
		VALUES($1,$2,$3,$4,0.9900,$5,$5)
		ON CONFLICT(session_id,student_record_id) DO UPDATE SET device_id=EXCLUDED.device_id,
		confidence=EXCLUDED.confidence,client_scanned_at=EXCLUDED.client_scanned_at,created_at=EXCLUDED.created_at`,
		institutionID, sessionID, student.id, deviceID, scannedAt)
}

func insertOverride(ctx context.Context, tx pgx.Tx, sessionID string, student demoStudent, status, reason string) error {
	return execSeed(ctx, tx, "demo attendance override", `
		INSERT INTO attendance_overrides(institution_id,session_id,student_record_id,status,reason,changed_by_user_id)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(session_id,student_record_id) DO UPDATE SET status=EXCLUDED.status,reason=EXCLUDED.reason,
		changed_by_user_id=EXCLUDED.changed_by_user_id`,
		institutionID, sessionID, student.id, status, reason, adminID)
}

func seedPendingWork(ctx context.Context, tx pgx.Tx, now time.Time) error {
	if err := execSeed(ctx, tx, "pending signup request", `
		INSERT INTO signup_requests(institution_id,matric_no,email,status)
		SELECT $1,'NEW-REQUEST-001','new-student@attendance.test','pending'
		WHERE NOT EXISTS(SELECT 1 FROM signup_requests WHERE institution_id=$1 AND matric_no='NEW-REQUEST-001' AND status='pending')`,
		institutionID); err != nil {
		return err
	}
	inviteHash := sha256.Sum256([]byte("attendance-demo-pending-invitation-token"))
	if err := execSeed(ctx, tx, "pending invitation", `
		INSERT INTO invitations(institution_id,email,role,scope_type,faculty_id,department_id,token_hash,expires_at)
		VALUES($1,'invited-lecturer@attendance.test','lecturer','department',NULL,$2,$3,$4)
		ON CONFLICT(token_hash) DO UPDATE SET expires_at=EXCLUDED.expires_at`,
		institutionID, departmentAID, inviteHash[:], now.Add(7*24*time.Hour)); err != nil {
		return err
	}
	return nil
}

func execSeed(ctx context.Context, tx pgx.Tx, name, query string, args ...any) error {
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("seed %s: %w", name, err)
	}
	return nil
}

func demoStudents() []demoStudent {
	legacy := []demoStudent{
		{id: student100AID, departmentID: departmentAID, programmeID: programmeAID, matric: "MAT100A", email: "student-100a@attendance.test", name: "Alex Demo", userID: studentUserID, password: "DemoStudent-2026!", level: 100, claimed: true},
		{id: student200AID, departmentID: departmentAID, programmeID: programmeAID, matric: "MAT200A", email: "student-200a@attendance.test", name: "Bailey Demo", level: 200},
		{id: student100BID, departmentID: departmentBID, programmeID: programmeBID, matric: "MAT100B", email: "student-100b@attendance.test", name: "Casey Demo", level: 100},
		{id: student200BID, departmentID: departmentBID, programmeID: programmeBID, matric: "MAT200B", email: "student-200b@attendance.test", name: "Drew Demo", level: 200},
	}
	students := append([]demoStudent(nil), legacy...)
	counts := map[string]int{departmentAID: 2, departmentBID: 2}
	for _, department := range demoDepartments {
		for counts[department.id] < 10 {
			studentIndex := len(students)
			level := []int{100, 200, 300, 400}[counts[department.id]%4]
			claimedLimit := 2
			if department.id == departmentAID {
				claimedLimit = 3
			} else if department.id == departmentBID {
				claimedLimit = 4
			}
			claimed := counts[department.id] < claimedLimit
			student := demoStudent{
				id: demoUUID(1000 + studentIndex), departmentID: department.id, programmeID: department.programmeID,
				matric: fmt.Sprintf("DEMO-%s-%03d", department.code, counts[department.id]+1),
				email:  fmt.Sprintf("student-%s-%02d@attendance.test", strings.ToLower(department.code), counts[department.id]+1),
				name:   fmt.Sprintf("%s Demo Student %02d", department.code, counts[department.id]+1),
				level:  level, claimed: claimed,
			}
			if claimed {
				student.userID = demoUUID(2000 + studentIndex)
				student.password = fmt.Sprintf("DemoStudent-%02d-2026!", studentIndex)
			}
			students = append(students, student)
			counts[department.id]++
		}
	}
	return students
}

func makeDemoCredentials() []demoCredential {
	credentials := []demoCredential{
		{"Platform administrator", platformAdminID, "platform-admin@attendance.test", "DemoPlatform-2026!", "platform_admin", "platform", "", ""},
		{"Institution administrator", adminID, "admin@attendance.test", "DemoAdmin-2026!", "institution_admin", "institution", "", ""},
		{"Engineering faculty administrator", facultyAdminID, "faculty-admin@attendance.test", "DemoFaculty-2026!", "faculty_admin", "faculty", facultyID, ""},
		{"Computing and Business faculty administrator", facultyAdminBID, "faculty-admin-cb@attendance.test", "DemoFacultyCB-2026!", "faculty_admin", "faculty", facultyBID, ""},
		{"ECE department administrator", deptAdminAID, "dept-a@attendance.test", "DemoDeptA-2026!", "department_admin", "department", "", departmentAID},
		{"Mechanical department administrator", deptAdminBID, "dept-b@attendance.test", "DemoDeptB-2026!", "department_admin", "department", "", departmentBID},
		{"Computer Science department administrator", deptAdminCID, "dept-c@attendance.test", "DemoDeptC-2026!", "department_admin", "department", "", departmentCID},
		{"Business department administrator", deptAdminDID, "dept-d@attendance.test", "DemoDeptD-2026!", "department_admin", "department", "", departmentDID},
	}
	for _, lecturer := range lecturers() {
		credentials = append(credentials, demoCredential{
			lecturer.name, lecturer.userID, lecturer.email, lecturer.password,
			"lecturer", "department", "", lecturer.departmentID,
		})
	}
	for _, student := range demoStudents() {
		if student.claimed {
			credentials = append(credentials, demoCredential{
				"Claimed student " + student.matric, student.userID, student.email, student.password,
				"student", "department", "", student.departmentID,
			})
		}
	}
	return credentials
}

type lecturerFixture struct {
	userID, email, password, name, departmentID, staffNo string
}

func lecturers() []lecturerFixture {
	return []lecturerFixture{
		{lecturerAID, "lecturer-a@attendance.test", "DemoLecturerA-2026!", "Lecturer A", departmentAID, "STAFF-ECE-01"},
		{lecturerBID, "lecturer-b@attendance.test", "DemoLecturerB-2026!", "Lecturer B", departmentBID, "STAFF-ME-01"},
		{lecturerCID, "lecturer-c@attendance.test", "DemoLecturerC-2026!", "Lecturer C", departmentCID, "STAFF-CSC-01"},
		{lecturerDID, "lecturer-d@attendance.test", "DemoLecturerD-2026!", "Lecturer D", departmentDID, "STAFF-BUS-01"},
	}
}

func academicPeriod(now time.Time) (time.Time, time.Time, time.Time, time.Time) {
	year := now.UTC().Year()
	if now.UTC().Month() < time.September {
		year--
	}
	start := time.Date(year, time.September, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year+1, time.August, 31, 0, 0, 0, 0, time.UTC)
	if now.UTC().Month() >= time.September || now.UTC().Month() == time.January {
		return start, end, start, time.Date(year+1, time.January, 31, 0, 0, 0, 0, time.UTC)
	}
	semesterStart := time.Date(year+1, time.February, 1, 0, 0, 0, 0, time.UTC)
	semesterEnd := end
	return start, end, semesterStart, semesterEnd
}

func weeklyAssignments(now time.Time) []teachingAssignment {
	assignments := make([]teachingAssignment, 0, len(demoCourses))
	for index, course := range demoCourses {
		dayOffset := index - 1
		if dayOffset < 0 {
			dayOffset = 0
		}
		date := now.UTC().AddDate(0, 0, dayOffset)
		start := time.Date(date.Year(), date.Month(), date.Day(), 9+index%6, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		if index == 0 {
			start = now.UTC().Add(-15 * time.Minute)
			end = now.UTC().Add(45 * time.Minute)
		} else if index == 1 {
			start = now.UTC().Add(2 * time.Hour)
			end = start.Add(time.Hour)
		}
		if start.Day() != now.UTC().Day() && index == 0 {
			start = time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
		}
		if end.Day() != now.UTC().Day() && index < 2 {
			end = time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 23, 59, 0, 0, time.UTC)
		}
		assignments = append(assignments, teachingAssignment{
			timetableID: demoUUID(3000 + index), offeringID: course.offeringID, venueID: course.venueID,
			weekday: int(date.Weekday()), startsAt: start.Format("15:04"), endsAt: end.Format("15:04"),
		})
	}
	return assignments
}

func assignmentsTimetableID(index int) string {
	if index == 0 {
		return timetable100ID
	}
	if index == 1 {
		return timetable200ID
	}
	return demoUUID(3000 + index)
}

func demoPlan() seedPlan {
	courses := map[string]bool{}
	levels := map[int]bool{}
	modes := map[string]bool{}
	for _, course := range demoCourses {
		courses[course.id] = true
		levels[course.level] = true
		modes[course.mode] = true
	}
	claimed := 0
	for _, student := range demoStudents() {
		levels[student.level] = true
		if student.claimed {
			claimed++
		}
	}
	return seedPlan{
		departments: len(demoDepartments), faculties: len(demoFaculties), programmes: len(demoDepartments),
		courses: len(courses), offerings: len(demoCourses), students: len(demoStudents()),
		claimedStudents: claimed, registrationModes: modes, levels: levels,
	}
}

type seedPlan struct {
	departments, faculties, programmes, courses, offerings, students, claimedStudents int
	registrationModes                                                                 map[string]bool
	levels                                                                            map[int]bool
}

func (p seedPlan) validate() error {
	if p.faculties != 2 || p.departments != 4 || p.programmes != 4 || p.courses < 10 ||
		p.offerings < 10 || p.students < 30 || p.students > 50 || p.claimedStudents == 0 ||
		p.claimedStudents >= p.students {
		return errors.New("demo seed must include two faculties, four departments/programmes, ten courses, and 30-50 students with claimed and unclaimed records")
	}
	for _, mode := range []string{"auto", "open", "approval", "closed"} {
		if !p.registrationModes[mode] {
			return fmt.Errorf("demo seed is missing registration mode %q", mode)
		}
	}
	for _, level := range []int{100, 200, 300, 400} {
		if !p.levels[level] {
			return fmt.Errorf("demo seed is missing level %d", level)
		}
	}
	return nil
}

func printCredentials(credentials []demoCredential) {
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "CAMPUS ATTENDANCE DEMO LOGINS (development only)")
	fmt.Fprintln(writer, "Role\tAccount\tEmail\tPassword")
	fmt.Fprintln(writer, "----\t-------\t-----\t--------")
	for _, credential := range credentials {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", credential.role, credential.label, credential.email, credential.password)
	}
	_ = writer.Flush()
	fmt.Println("\nInstitution ID:", institutionID)
	fmt.Println("Claimed students have accounts; every unclaimed record can be claimed with its matric/email pair.")
}

func printUnclaimedStudents() {
	fmt.Println("\nUNCLAIMED STUDENT RECORDS (matric number / institution email)")
	for _, student := range demoStudents() {
		if !student.claimed {
			fmt.Printf("%s\t%s\n", student.matric, student.email)
		}
	}
}

func demoUUID(value int) string {
	return fmt.Sprintf("10000000-0000-4000-8000-%012x", value)
}

func demoStudentInDepartment(departmentID string, level int, skipID string) demoStudent {
	for _, student := range demoStudents() {
		if student.departmentID == departmentID && student.level == level && student.id != skipID {
			return student
		}
	}
	return demoStudent{}
}
