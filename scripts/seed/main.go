package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"attendance-api/internal/scans"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	institutionID = "10000000-0000-4000-8000-000000000001"
	facultyID     = "10000000-0000-4000-8000-000000000002"
	departmentAID = "10000000-0000-4000-8000-000000000003"
	departmentBID = "10000000-0000-4000-8000-000000000004"
	programmeAID  = "10000000-0000-4000-8000-000000000005"
	programmeBID  = "10000000-0000-4000-8000-000000000006"
	academicID    = "10000000-0000-4000-8000-000000000007"
	semesterID    = "10000000-0000-4000-8000-000000000008"
	courseID      = "10000000-0000-4000-8000-000000000009"
	offering100ID = "10000000-0000-4000-8000-000000000010"
	offering200ID = "10000000-0000-4000-8000-000000000011"
	venueAID      = "10000000-0000-4000-8000-000000000012"
	venueBID      = "10000000-0000-4000-8000-000000000013"
	deviceID      = "10000000-0000-4000-8000-000000000014"
	student100AID = "10000000-0000-4000-8000-000000000015"
	student200AID = "10000000-0000-4000-8000-000000000016"
	student100BID = "10000000-0000-4000-8000-000000000017"
	student200BID = "10000000-0000-4000-8000-000000000018"
	adminID       = "10000000-0000-4000-8000-000000000019"
	deptAdminAID  = "10000000-0000-4000-8000-000000000020"
	deptAdminBID  = "10000000-0000-4000-8000-000000000021"
	lecturerAID   = "10000000-0000-4000-8000-000000000022"
	lecturerBID   = "10000000-0000-4000-8000-000000000023"
	studentUserID = "10000000-0000-4000-8000-000000000024"
)

type demoCredential struct {
	label    string
	userID   string
	email    string
	password string
	role     string
	deptID   string
}

var demoCredentials = []demoCredential{
	{"Institution administrator", adminID, "admin@attendance.test", "DemoAdmin-2026!", "institution_admin", ""},
	{"Department A administrator", deptAdminAID, "dept-a@attendance.test", "DemoDeptA-2026!", "department_admin", departmentAID},
	{"Department B administrator", deptAdminBID, "dept-b@attendance.test", "DemoDeptB-2026!", "department_admin", departmentBID},
	{"Lecturer A", lecturerAID, "lecturer-a@attendance.test", "DemoLecturerA-2026!", "lecturer", departmentAID},
	{"Lecturer B", lecturerBID, "lecturer-b@attendance.test", "DemoLecturerB-2026!", "lecturer", departmentBID},
	{"Student (claimed record)", studentUserID, "student-100a@attendance.test", "DemoStudent-2026!", "student", departmentAID},
}

type seedPlan struct {
	departments []string
	levels      []int
	records     int
	general     bool
	offerings   []int
}

func demoPlan() seedPlan {
	return seedPlan{
		departments: []string{departmentAID, departmentBID},
		levels:      []int{100, 200},
		records:     4,
		general:     true,
		offerings:   []int{100, 200},
	}
}

func (p seedPlan) validate() error {
	if len(p.departments) != 2 || len(p.levels) != 2 || p.records != 4 ||
		!p.general || len(p.offerings) != len(p.levels) {
		return errors.New("demo dataset must contain two departments, two levels, a general course, and four student records")
	}
	if p.levels[0] == p.levels[1] || p.offerings[0] != p.levels[0] || p.offerings[1] != p.levels[1] {
		return errors.New("demo offerings must cover each distinct student level")
	}
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
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

	hashedPasswords := make(map[string][]byte, len(demoCredentials))
	for _, credential := range demoCredentials {
		hash, err := bcrypt.GenerateFromPassword([]byte(credential.password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash demo password for %s: %w", credential.label, err)
		}
		hashedPasswords[credential.userID] = hash
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
	deviceCreated, err := seedDemoData(ctx, tx, hashedPasswords, deviceHash)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit demo data: %w", err)
	}

	printCredentials()
	if deviceCreated {
		fmt.Printf("\nRegistered device key (shown only on initial creation): %s\n", deviceKey)
	} else {
		fmt.Println("\nThe demo device already exists; its key was not rotated or displayed.")
	}
	return nil
}

func seedDemoData(ctx context.Context, tx pgx.Tx, hashes map[string][]byte, deviceHash []byte) (bool, error) {
	queries := []struct {
		name  string
		query string
		args  []any
	}{
		{"institution", `INSERT INTO institutions(id,name) VALUES($1,'Northbridge Campus Institute')
			ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name`, []any{institutionID}},
		{"faculty", `INSERT INTO faculties(id,institution_id,name) VALUES($1,$2,'Faculty of Engineering')
			ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name`, []any{facultyID, institutionID}},
		{"department A", `INSERT INTO departments(id,institution_id,faculty_id,name,code)
			VALUES($1,$2,$3,'Electrical and Computer Engineering','ECE')
			ON CONFLICT(id) DO UPDATE SET faculty_id=EXCLUDED.faculty_id,name=EXCLUDED.name,code=EXCLUDED.code`,
			[]any{departmentAID, institutionID, facultyID}},
		{"department B", `INSERT INTO departments(id,institution_id,faculty_id,name,code)
			VALUES($1,$2,$3,'Mechanical Engineering','ME')
			ON CONFLICT(id) DO UPDATE SET faculty_id=EXCLUDED.faculty_id,name=EXCLUDED.name,code=EXCLUDED.code`,
			[]any{departmentBID, institutionID, facultyID}},
		{"programme A", `INSERT INTO programmes(id,institution_id,department_id,name,code)
			VALUES($1,$2,$3,'Computer Engineering','CPE')
			ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,name=EXCLUDED.name,code=EXCLUDED.code`,
			[]any{programmeAID, institutionID, departmentAID}},
		{"programme B", `INSERT INTO programmes(id,institution_id,department_id,name,code)
			VALUES($1,$2,$3,'Mechanical Engineering','MEC')
			ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,name=EXCLUDED.name,code=EXCLUDED.code`,
			[]any{programmeBID, institutionID, departmentBID}},
		{"academic session", `INSERT INTO academic_sessions(id,institution_id,name,starts_on,ends_on,current)
			VALUES($1,$2,'2026-2027','2026-09-01','2027-08-31',true)
			ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,starts_on=EXCLUDED.starts_on,ends_on=EXCLUDED.ends_on,current=true`,
			[]any{academicID, institutionID}},
		{"semester", `INSERT INTO semesters(id,institution_id,academic_session_id,name,starts_on,ends_on)
			VALUES($1,$2,$3,'Semester 1','2026-09-01','2027-01-31')
			ON CONFLICT(id) DO UPDATE SET academic_session_id=EXCLUDED.academic_session_id,name=EXCLUDED.name,
			starts_on=EXCLUDED.starts_on,ends_on=EXCLUDED.ends_on`,
			[]any{semesterID, institutionID, academicID}},
		{"course", `INSERT INTO courses(id,institution_id,department_id,code,title,kind,description,credits)
			VALUES($1,$2,$3,'ECE301','Circuits and Systems','general','Introductory circuit analysis for both departments',3)
			ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,code=EXCLUDED.code,title=EXCLUDED.title,
			kind=EXCLUDED.kind,description=EXCLUDED.description,credits=EXCLUDED.credits`,
			[]any{courseID, institutionID, departmentAID}},
		{"offering level 100", `INSERT INTO course_offerings(id,institution_id,course_id,semester_id,coordinator_user_id,
			level,registration_mode,roster_policy,min_attendance_pct,late_counts_as_attended)
			VALUES($1,$2,$3,$4,$5,100,'auto','registered_only',75,true)
			ON CONFLICT(id) DO UPDATE SET course_id=EXCLUDED.course_id,semester_id=EXCLUDED.semester_id,
			coordinator_user_id=EXCLUDED.coordinator_user_id,level=EXCLUDED.level,registration_mode=EXCLUDED.registration_mode`,
			[]any{offering100ID, institutionID, courseID, semesterID, lecturerAID}},
		{"offering level 200", `INSERT INTO course_offerings(id,institution_id,course_id,semester_id,coordinator_user_id,
			level,registration_mode,roster_policy,min_attendance_pct,late_counts_as_attended)
			VALUES($1,$2,$3,$4,$5,200,'auto','registered_only',75,true)
			ON CONFLICT(id) DO UPDATE SET course_id=EXCLUDED.course_id,semester_id=EXCLUDED.semester_id,
			coordinator_user_id=EXCLUDED.coordinator_user_id,level=EXCLUDED.level,registration_mode=EXCLUDED.registration_mode`,
			[]any{offering200ID, institutionID, courseID, semesterID, lecturerBID}},
		{"venue A", `INSERT INTO venues(id,institution_id,name,latitude,longitude,radius_meters)
			VALUES($1,$2,'Engineering Hall',6.5244,3.3792,150)
			ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,latitude=EXCLUDED.latitude,
			longitude=EXCLUDED.longitude,radius_meters=EXCLUDED.radius_meters`, []any{venueAID, institutionID}},
		{"venue B", `INSERT INTO venues(id,institution_id,name,latitude,longitude,radius_meters)
			VALUES($1,$2,'Workshop Building',6.5251,3.3810,120)
			ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,latitude=EXCLUDED.latitude,
			longitude=EXCLUDED.longitude,radius_meters=EXCLUDED.radius_meters`, []any{venueBID, institutionID}},
	}
	if _, err := tx.Exec(ctx, queries[0].query, queries[0].args...); err != nil {
		return false, fmt.Errorf("seed %s: %w", queries[0].name, err)
	}
	verifiedAt := time.Now().UTC()
	for _, credential := range demoCredentials {
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,institution_id,email,password_hash,display_name,email_verified_at)
			VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(id) DO UPDATE SET email=EXCLUDED.email,password_hash=EXCLUDED.password_hash,
			display_name=EXCLUDED.display_name,email_verified_at=EXCLUDED.email_verified_at,disabled_at=NULL`,
			credential.userID, institutionID, credential.email, hashes[credential.userID], credential.label, verifiedAt); err != nil {
			return false, fmt.Errorf("seed demo user %s: %w", credential.label, err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE academic_sessions SET current=false
		WHERE institution_id=$1 AND id<>$2 AND current=true`, institutionID, academicID); err != nil {
		return false, fmt.Errorf("clear previous current academic session: %w", err)
	}
	for _, item := range queries[1:] {
		if _, err := tx.Exec(ctx, item.query, item.args...); err != nil {
			return false, fmt.Errorf("seed %s: %w", item.name, err)
		}
	}
	for _, role := range demoCredentials {
		scopeType := "institution"
		var department any
		if role.deptID != "" {
			scopeType = "department"
			department = role.deptID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO role_assignments(institution_id,user_id,role,scope_type,department_id)
			VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
			institutionID, role.userID, role.role, scopeType, department); err != nil {
			return false, fmt.Errorf("seed role for %s: %w", role.label, err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO student_records(id,institution_id,department_id,programme_id,matric_no,email,
		display_name,level,claimed_by_user_id) VALUES
		($1,$5,$6,$8,'MAT100A','student-100a@attendance.test','Alex Demo',100,$10),
		($2,$5,$6,$8,'MAT200A','student-200a@attendance.test','Bailey Demo',200,NULL),
		($3,$5,$7,$9,'MAT100B','student-100b@attendance.test','Casey Demo',100,NULL),
		($4,$5,$7,$9,'MAT200B','student-200b@attendance.test','Drew Demo',200,NULL)
		ON CONFLICT(id) DO UPDATE SET department_id=EXCLUDED.department_id,programme_id=EXCLUDED.programme_id,
		matric_no=EXCLUDED.matric_no,email=EXCLUDED.email,display_name=EXCLUDED.display_name,level=EXCLUDED.level,
		claimed_by_user_id=coalesce(student_records.claimed_by_user_id,EXCLUDED.claimed_by_user_id)`,
		student100AID, student200AID, student100BID, student200BID,
		institutionID, departmentAID, departmentBID, programmeAID, programmeBID, studentUserID); err != nil {
		return false, fmt.Errorf("seed imported student records: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO staff_records(institution_id,department_id,staff_no,email,display_name,user_id)
		VALUES($1,$2,'STAFF-ECE-01','lecturer-a@attendance.test','Lecturer A',$3),
		      ($1,$4,'STAFF-ME-01','lecturer-b@attendance.test','Lecturer B',$5)
		ON CONFLICT(institution_id,staff_no) DO UPDATE SET user_id=EXCLUDED.user_id,
		email=EXCLUDED.email,display_name=EXCLUDED.display_name`,
		institutionID, departmentAID, lecturerAID, departmentBID, lecturerBID); err != nil {
		return false, fmt.Errorf("seed lecturer records: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO offering_lecturers(institution_id,offering_id,lecturer_user_id)
		VALUES($1,$2,$4),($1,$3,$5) ON CONFLICT DO NOTHING`,
		institutionID, offering100ID, offering200ID, lecturerAID, lecturerBID); err != nil {
		return false, fmt.Errorf("seed offering lecturers: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO offering_auto_targets(institution_id,offering_id,department_id,level)
		VALUES($1,$2,$4,100),($1,$2,$5,100),($1,$3,$4,200),($1,$3,$5,200)
		ON CONFLICT DO NOTHING`, institutionID, offering100ID, offering200ID, departmentAID, departmentBID); err != nil {
		return false, fmt.Errorf("seed offering auto-registration targets: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO course_registrations(institution_id,offering_id,student_record_id,status,approved_at)
		VALUES($1,$2,$4,'approved',now()),($1,$2,$5,'approved',now()),
		      ($1,$3,$6,'approved',now()),($1,$3,$7,'approved',now())
		ON CONFLICT(institution_id,offering_id,student_record_id) DO UPDATE SET
		status='approved',approved_at=coalesce(course_registrations.approved_at,now())`,
		institutionID, offering100ID, offering200ID, student100AID, student100BID, student200AID, student200BID); err != nil {
		return false, fmt.Errorf("seed course registrations: %w", err)
	}

	var createdID string
	err := tx.QueryRow(ctx, `INSERT INTO devices(id,institution_id,venue_id,name,key_hash)
		VALUES($1,$2,$3,'Engineering Hall Scanner',$4)
		ON CONFLICT(institution_id,id) DO NOTHING
		RETURNING id::text`, deviceID, institutionID, venueAID, deviceHash).Scan(&createdID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("seed demo device: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO device_slots(institution_id,device_id,student_record_id,slot_number)
		VALUES($1,$2,$3,1) ON CONFLICT(institution_id,device_id,slot_number)
		DO UPDATE SET student_record_id=EXCLUDED.student_record_id`,
		institutionID, deviceID, student100AID); err != nil {
		return false, fmt.Errorf("seed demo device slot: %w", err)
	}
	return true, nil
}

func printCredentials() {
	fmt.Println("Campus Attendance demo credentials (development only):")
	for _, credential := range demoCredentials {
		fmt.Printf("  %-30s email=%s password=%s\n", credential.label, credential.email, credential.password)
	}
	fmt.Println("\nInstitution ID:", institutionID)
	fmt.Printf("Departments: ECE=%s ME=%s\n", departmentAID, departmentBID)
	fmt.Printf("Offerings: level 100=%s level 200=%s\n", offering100ID, offering200ID)
	fmt.Printf("Claimed demo student record: MAT100A=%s\n", student100AID)
	fmt.Printf("Unclaimed records: MAT200A=%s student-200a@attendance.test; MAT100B=%s student-100b@attendance.test; MAT200B=%s student-200b@attendance.test\n",
		student200AID, student100BID, student200BID)
	fmt.Println("General course: ECE301 - Circuits and Systems (levels 100 and 200)")
	fmt.Println("Venue A ID:", venueAID)
	fmt.Println("Venue B ID:", venueBID)
	fmt.Println("OpenAPI and manual test steps are documented in api/openapi.yaml and docs/MANUAL_TEST_GUIDE.md.")
}
