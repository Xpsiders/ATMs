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
		{"web only with scanner", Record{OnRoster: true, HasWebCheckin: true, ScannerAvailable: true}, WebOnlyUnverified, "present"},
		{"scan only", Record{OnRoster: true, HasFingerprint: true, ScannerAvailable: true}, PresentNoWebLog, "present"},
		{"absent", Record{OnRoster: true, ScannerAvailable: true}, Absent, "absent"},
		{"web only without scanner", Record{OnRoster: true, HasWebCheckin: true}, Verified, "present"},
		{"late web check-in", Record{OnRoster: true, HasWebCheckin: true, ScannerAvailable: true, WebStatus: "late"}, WebOnlyUnverified, "late"},
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
		{"weak fix is unverified, not a suspected proxy", Record{
			OnRoster: true, HasWebCheckin: true, ScannerAvailable: true,
			LocationUnverified: true,
		}, WebOnlyUnverified, "present"},
		{"a scan verifies a weak fix outright", Record{
			OnRoster: true, HasWebCheckin: true, HasFingerprint: true,
			ScannerAvailable: true, LocationUnverified: true,
		}, Verified, "present"},
		{"an override beats a weak fix", Record{
			OnRoster: true, HasWebCheckin: true, ScannerAvailable: true,
			LocationUnverified: true, OverrideStatus: "present",
		}, Verified, "present"},
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

// A weak phone fix is reported with its own reason so the lecturer can tell it
// apart from a scanner outage.
func TestClassifyReportsLocationUnverifiedReason(t *testing.T) {
	t.Parallel()
	outcome := Classify(Record{
		OnRoster: true, HasWebCheckin: true, ScannerAvailable: true,
		LocationUnverified: true,
		ScannerCoverage:    ScannerCoverage{State: CoverageFull},
	})
	if outcome.Classification != WebOnlyUnverified || outcome.Reason != "location_unverified" {
		t.Fatalf("Classify() = %+v, want web_only_unverified/location_unverified", outcome)
	}
	outcome = Classify(Record{
		OnRoster: true, HasWebCheckin: true, ScannerAvailable: true,
		LocationUnverified: true,
		ScannerCoverage:    ScannerCoverage{State: CoverageNone},
	})
	if outcome.Reason != "scanner_unavailable" {
		t.Fatalf("a scanner outage should keep its own reason, got %q", outcome.Reason)
	}
}

// A student who checked in on the web while a working scanner stood in the
// room is reported as unverified with its own reason, never accused of a
// proxy: the server cannot tell a stand-in from a finger the sensor would not
// read, and the check-in still passed a live code and the device binding.
func TestClassifyReportsMissingFingerprintWithoutAccusation(t *testing.T) {
	t.Parallel()
	outcome := Classify(Record{
		OnRoster: true, HasWebCheckin: true, ScannerAvailable: true,
		ScannerCoverage: ScannerCoverage{State: CoverageFull},
	})
	if outcome.Classification != WebOnlyUnverified || outcome.Reason != "no_fingerprint" {
		t.Fatalf("Classify() = %+v, want web_only_unverified/no_fingerprint", outcome)
	}
	if outcome.Status != "present" {
		t.Fatalf("an unverified row should still count as present, got %q", outcome.Status)
	}
	// A fingerprint, or an override, still outranks the flag.
	verified := Classify(Record{
		OnRoster: true, HasWebCheckin: true, HasFingerprint: true, ScannerAvailable: true,
		ScannerCoverage: ScannerCoverage{State: CoverageFull},
	})
	if verified.Classification != Verified || verified.Reason != "" {
		t.Fatalf("a scanned check-in should be verified, got %+v", verified)
	}
}
