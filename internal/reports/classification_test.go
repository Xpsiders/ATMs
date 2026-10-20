package reports

import "testing"
import "time"

func TestClassificationMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record Record
		want   Classification
		status string
	}{
		{"web and scan", Record{OnRoster: true, HasWebCheckin: true, HasFingerprint: true, ScannerAvailable: true}, Verified, "present"},
		{"web only with scanner", Record{OnRoster: true, HasWebCheckin: true, ScannerAvailable: true}, SuspectedProxy, "present"},
		{"scan only", Record{OnRoster: true, HasFingerprint: true, ScannerAvailable: true}, PresentNoWebLog, "present"},
		{"absent", Record{OnRoster: true, ScannerAvailable: true}, Absent, "absent"},
		{"web only without scanner", Record{OnRoster: true, HasWebCheckin: true}, Verified, "present"},
		{"late web check-in", Record{OnRoster: true, HasWebCheckin: true, ScannerAvailable: true, WebStatus: "late"}, SuspectedProxy, "late"},
		{"unrostered still reported", Record{HasFingerprint: true, ScannerAvailable: true}, PresentNoWebLog, "present"},
		{"present override wins", Record{HasFingerprint: true, ScannerAvailable: true, OverrideStatus: "present"}, Verified, "present"},
		{"absent override wins", Record{HasWebCheckin: true, ScannerAvailable: true, OverrideStatus: "absent"}, Absent, "absent"},
		{"late override", Record{OverrideStatus: "late"}, Verified, "late"},
		{"excused override", Record{OverrideStatus: "excused"}, Classification("excused"), "excused"},
		{"web only in offline interval", Record{
			OnRoster: true, HasWebCheckin: true, ScannerAvailable: true,
			WebCheckinAt: timePointer(time.Unix(120, 0)),
			ScannerCoverage: ScannerCoverage{
				State:            CoveragePartial,
				OfflineIntervals: []OfflineInterval{{From: time.Unix(100, 0), To: time.Unix(130, 0)}},
			},
		}, WebOnlyUnverified, "present"},
		{"web only when all scanners offline", Record{
			OnRoster: true, HasWebCheckin: true, ScannerAvailable: true,
			ScannerCoverage: ScannerCoverage{State: CoverageNone},
		}, WebOnlyUnverified, "present"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Classify(tc.record)
			if got.Classification != tc.want || got.Status != tc.status || got.OnRoster != tc.record.OnRoster {
				t.Fatalf("Classify() = %+v, want classification=%s status=%s on_roster=%v",
					got, tc.want, tc.status, tc.record.OnRoster)
			}
		})
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestScannerCoverageStateCalculation(t *testing.T) {
	t.Parallel()
	start := time.Unix(100, 0).UTC()
	asOf := time.Unix(200, 0).UTC()
	after := time.Duration(20) * time.Second
	tests := []struct {
		name      string
		input     ScannerCoverageInput
		state     ScannerCoverageState
		intervals int
	}{
		{"no scanner", ScannerCoverageInput{SessionStart: start, AsOf: asOf}, CoverageNoScanner, 0},
		{"full", ScannerCoverageInput{SessionStart: start, AsOf: asOf, OfflineAfter: after,
			Heartbeats: [][]time.Time{{time.Unix(95, 0), time.Unix(110, 0), time.Unix(130, 0),
				time.Unix(150, 0), time.Unix(170, 0), time.Unix(190, 0)}}}, CoverageFull, 0},
		{"partial outage", ScannerCoverageInput{SessionStart: start, AsOf: asOf, OfflineAfter: after,
			Heartbeats: [][]time.Time{{time.Unix(95, 0), time.Unix(105, 0), time.Unix(150, 0),
				time.Unix(180, 0), time.Unix(200, 0)}}}, CoveragePartial, 2},
		{"none after stale heartbeat", ScannerCoverageInput{SessionStart: start, AsOf: asOf, OfflineAfter: after,
			Heartbeats: [][]time.Time{{time.Unix(50, 0)}}}, CoverageNone, 1},
		{"manual unavailable", ScannerCoverageInput{SessionStart: start, AsOf: asOf, OfflineAfter: after,
			Heartbeats: [][]time.Time{{time.Unix(95, 0), time.Unix(105, 0), time.Unix(140, 0), time.Unix(190, 0)}}, Unavailable: true},
			CoverageNone, 1},
		{"one scanner covers another outage", ScannerCoverageInput{SessionStart: start, AsOf: asOf, OfflineAfter: after,
			Heartbeats: [][]time.Time{
				{time.Unix(95, 0), time.Unix(105, 0), time.Unix(150, 0), time.Unix(180, 0), time.Unix(200, 0)},
				{time.Unix(95, 0), time.Unix(105, 0), time.Unix(125, 0), time.Unix(150, 0), time.Unix(170, 0), time.Unix(190, 0), time.Unix(200, 0)},
			}}, CoveragePartial, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coverage := ComputeScannerCoverage(tc.input)
			if coverage.State != tc.state || len(coverage.OfflineIntervals) != tc.intervals {
				t.Fatalf("coverage = %+v, want state %s and %d intervals", coverage, tc.state, tc.intervals)
			}
		})
	}
}

func TestRosterSheetDepartmentScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		full       bool
		scope      string
		requested  string
		want       string
		authorized bool
	}{
		{"full roster no filter", true, "", "", "", true},
		{"full roster requested filter", true, "", "dept-a", "dept-a", true},
		{"department scoped roster", false, "dept-b", "", "dept-b", true},
		{"matching filter", false, "dept-b", "dept-b", "dept-b", true},
		{"cannot widen department scope", false, "dept-b", "dept-a", "", false},
		{"missing department scope", false, "", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, authorized := ResolveRosterDepartment(tc.full, tc.scope, tc.requested)
			if got != tc.want || authorized != tc.authorized {
				t.Fatalf("ResolveRosterDepartment() = %q, %v; want %q, %v",
					got, authorized, tc.want, tc.authorized)
			}
		})
	}
}

func TestAttendancePercentageAndThreshold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		statuses   []AttendanceStatus
		lateCounts bool
		want       float64
	}{
		{"zero sessions", nil, true, 0},
		{"late counts", []AttendanceStatus{StatusPresent, StatusLate, StatusAbsent, StatusExcused}, true, 50},
		{"late does not count", []AttendanceStatus{StatusPresent, StatusLate}, false, 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AttendancePercentage(tc.statuses, tc.lateCounts); got != tc.want {
				t.Fatalf("AttendancePercentage() = %v, want %v", got, tc.want)
			}
		})
	}
	if BelowThreshold([]AttendanceStatus{StatusPresent, StatusAbsent, StatusAbsent, StatusAbsent}, true, 25) {
		t.Fatal("exact threshold should not be below threshold")
	}
	if !BelowThreshold([]AttendanceStatus{StatusAbsent, StatusAbsent}, true, 75) {
		t.Fatal("zero attendance should be below threshold")
	}
}
