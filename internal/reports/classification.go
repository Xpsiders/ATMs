package reports

import (
	"sort"
	"time"
)

type Classification string

const (
	Verified Classification = "verified"
	// There is deliberately no "suspected proxy" classification. Nothing the
	// server observes distinguishes a proxy from a student whose scan failed,
	// so an unverified row is surfaced for the lecturer to judge instead of
	// being labelled an accusation the evidence cannot support.
	PresentNoWebLog   Classification = "present_no_web_log"
	WebOnlyUnverified Classification = "web_only_unverified"
	Absent            Classification = "absent"
)

type OfflineInterval struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type ScannerCoverageState string

const (
	CoverageFull      ScannerCoverageState = "full"
	CoveragePartial   ScannerCoverageState = "partial"
	CoverageNone      ScannerCoverageState = "none"
	CoverageNoScanner ScannerCoverageState = "no_scanner"
)

type ScannerCoverage struct {
	State            ScannerCoverageState `json:"state"`
	OfflineIntervals []OfflineInterval    `json:"offline_intervals"`
}

type ScannerCoverageInput struct {
	SessionStart  time.Time
	SessionEnd    time.Time
	AsOf          time.Time
	OfflineAfter  time.Duration
	Heartbeats    [][]time.Time
	Unavailable   bool
	UnavailableAt *time.Time
}

func ResolveRosterDepartment(fullAccess bool, scopedDepartment, requestedDepartment string) (string, bool) {
	if fullAccess {
		return requestedDepartment, true
	}
	if scopedDepartment == "" || (requestedDepartment != "" && requestedDepartment != scopedDepartment) {
		return "", false
	}
	return scopedDepartment, true
}

func ComputeScannerCoverage(input ScannerCoverageInput) ScannerCoverage {
	if len(input.Heartbeats) == 0 {
		return ScannerCoverage{State: CoverageNoScanner, OfflineIntervals: []OfflineInterval{}}
	}
	start := input.SessionStart.UTC()
	end := input.SessionEnd.UTC()
	if end.IsZero() || end.After(input.AsOf.UTC()) {
		end = input.AsOf.UTC()
	}
	if !end.After(start) {
		return ScannerCoverage{State: CoverageFull, OfflineIntervals: []OfflineInterval{}}
	}
	if input.Unavailable {
		return ScannerCoverage{
			State:            CoverageNone,
			OfflineIntervals: []OfflineInterval{{From: start, To: end}},
		}
	}
	if input.OfflineAfter <= 0 {
		return ScannerCoverage{State: CoverageFull, OfflineIntervals: []OfflineInterval{}}
	}

	var outageSets [][]OfflineInterval
	for _, values := range input.Heartbeats {
		times := append([]time.Time(nil), values...)
		sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
		previous := start.Add(-input.OfflineAfter)
		hasHeartbeatBeforeSession := false
		var deviceOutages []OfflineInterval
		for _, heartbeat := range times {
			heartbeat = heartbeat.UTC()
			if heartbeat.Before(start) {
				previous = heartbeat
				hasHeartbeatBeforeSession = true
				continue
			}
			if heartbeat.After(end) {
				break
			}
			outageStart := previous.Add(input.OfflineAfter)
			if outageStart.Before(start) {
				outageStart = start
			}
			if !hasHeartbeatBeforeSession && previous.Before(start) {
				outageStart = start.Add(input.OfflineAfter)
			}
			if heartbeat.After(outageStart) {
				deviceOutages = append(deviceOutages, OfflineInterval{From: outageStart, To: heartbeat})
			}
			previous = heartbeat
			hasHeartbeatBeforeSession = true
		}
		if !hasHeartbeatBeforeSession {
			outageStart := start.Add(input.OfflineAfter)
			if end.After(outageStart) {
				deviceOutages = append(deviceOutages, OfflineInterval{From: outageStart, To: end})
			}
		} else {
			outageStart := previous.Add(input.OfflineAfter)
			if outageStart.Before(start) {
				outageStart = start
			}
			if end.After(outageStart) {
				deviceOutages = append(deviceOutages, OfflineInterval{From: outageStart, To: end})
			}
		}
		outageSets = append(outageSets, deviceOutages)
	}
	intervals := intersectOutages(outageSets)
	if input.UnavailableAt != nil {
		from := input.UnavailableAt.UTC()
		if from.Before(start) {
			from = start
		}
		if end.After(from) {
			intervals = mergeIntervals(append(intervals, OfflineInterval{From: from, To: end}))
		}
	}
	state := CoverageFull
	if len(intervals) > 0 {
		state = CoveragePartial
		if len(intervals) == 1 && !intervals[0].From.After(start) && !intervals[0].To.Before(end) {
			state = CoverageNone
		}
	}
	return ScannerCoverage{State: state, OfflineIntervals: intervals}
}

func intersectOutages(sets [][]OfflineInterval) []OfflineInterval {
	if len(sets) == 0 {
		return []OfflineInterval{}
	}
	result := sets[0]
	for _, outages := range sets[1:] {
		var intersections []OfflineInterval
		for _, left := range result {
			for _, right := range outages {
				from := left.From
				if right.From.After(from) {
					from = right.From
				}
				to := left.To
				if right.To.Before(to) {
					to = right.To
				}
				if to.After(from) {
					intersections = append(intersections, OfflineInterval{From: from, To: to})
				}
			}
		}
		result = intersections
	}
	return mergeIntervals(result)
}

func mergeIntervals(intervals []OfflineInterval) []OfflineInterval {
	if len(intervals) == 0 {
		return []OfflineInterval{}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].From.Before(intervals[j].From) })
	merged := []OfflineInterval{intervals[0]}
	for _, interval := range intervals[1:] {
		last := &merged[len(merged)-1]
		if !interval.From.After(last.To) {
			if interval.To.After(last.To) {
				last.To = interval.To
			}
			continue
		}
		merged = append(merged, interval)
	}
	return merged
}

type Record struct {
	OnRoster         bool
	HasWebCheckin    bool
	HasFingerprint   bool
	ScannerAvailable bool
	OverrideStatus   string
	WebStatus        string
	WebCheckinAt     *time.Time
	ScannerCoverage  ScannerCoverage
	// LocationUnverified marks a web check-in whose position landed further
	// from the venue than the phone's own reported error can explain. It
	// sharpens the reason on an unverified row; it is not an accusation on its
	// own, and it never refuses the check-in.
	LocationUnverified bool
}

type Outcome struct {
	Classification Classification `json:"classification"`
	Status         string         `json:"status"`
	OnRoster       bool           `json:"on_roster"`
	Reason         string         `json:"reason,omitempty"`
}

func Classify(record Record) Outcome {
	outcome := Outcome{OnRoster: record.OnRoster}
	if record.OverrideStatus != "" {
		outcome.Status = record.OverrideStatus
		switch record.OverrideStatus {
		case "absent":
			outcome.Classification = Absent
		case "excused":
			outcome.Classification = "excused"
		default:
			outcome.Classification = Verified
		}
		return outcome
	}

	switch {
	case record.HasWebCheckin && record.HasFingerprint:
		outcome.Classification, outcome.Status = Verified, webAttendanceStatus(record.WebStatus)
	case record.HasWebCheckin && !record.ScannerAvailable:
		outcome.Classification, outcome.Status = Verified, webAttendanceStatus(record.WebStatus)
	case record.HasWebCheckin:
		outcome.Status = webAttendanceStatus(record.WebStatus)
		if record.ScannerCoverage.State == CoverageNone {
			outcome.Classification, outcome.Reason = WebOnlyUnverified, "scanner_unavailable"
		} else if isOfflineCheckin(record.WebCheckinAt, record.ScannerCoverage.OfflineIntervals) {
			outcome.Classification, outcome.Reason = WebOnlyUnverified, "scanner_offline"
		} else if record.LocationUnverified {
			outcome.Classification, outcome.Reason = WebOnlyUnverified, "location_unverified"
		} else {
			// A working scanner was there and the student did not use it. That
			// is worth the lecturer's attention, but it is not evidence of a
			// proxy: the check-in still passed a live rotating code and the
			// device binding that stops one handset signing in a row of
			// friends. Queues, a finger the sensor will not read, and a
			// student who simply did not know to scan all look identical from
			// here, so the row is reported as unverified rather than accused.
			outcome.Classification, outcome.Reason = WebOnlyUnverified, "no_fingerprint"
		}
	case record.HasFingerprint:
		outcome.Classification, outcome.Status = PresentNoWebLog, "present"
	default:
		outcome.Classification, outcome.Status = Absent, "absent"
	}
	return outcome
}

func isOfflineCheckin(at *time.Time, intervals []OfflineInterval) bool {
	if at == nil {
		return false
	}
	for _, interval := range intervals {
		if !at.Before(interval.From) && at.Before(interval.To) {
			return true
		}
	}
	return false
}

func webAttendanceStatus(status string) string {
	if status == "late" {
		return "late"
	}
	return "present"
}

type AttendanceStatus string

const (
	StatusPresent AttendanceStatus = "present"
	StatusLate    AttendanceStatus = "late"
	StatusAbsent  AttendanceStatus = "absent"
	StatusExcused AttendanceStatus = "excused"
)

func AttendancePercentage(statuses []AttendanceStatus, lateCounts bool) float64 {
	if len(statuses) == 0 {
		return 0
	}
	attended := 0
	for _, status := range statuses {
		if status == StatusPresent || (status == StatusLate && lateCounts) {
			attended++
		}
	}
	return float64(attended) * 100 / float64(len(statuses))
}

func BelowThreshold(statuses []AttendanceStatus, lateCounts bool, minimum float64) bool {
	return AttendancePercentage(statuses, lateCounts) < minimum
}
