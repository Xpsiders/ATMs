package checkins

import (
	"math"
	"testing"
	"time"
)

func TestHaversineMeters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		lat1, lon1 float64
		lat2, lon2 float64
		wantMeters float64
		toleranceM float64
		wantErr    bool
	}{
		{"zero", 0, 0, 0, 0, 0, 0, false},
		{"known one degree", 0, 0, 0, 1, 111_195, 10, false},
		{"invalid latitude", 91, 0, 0, 0, 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := HaversineMeters(tc.lat1, tc.lon1, tc.lat2, tc.lon2)
			if (err != nil) != tc.wantErr {
				t.Fatalf("HaversineMeters() error = %v", err)
			}
			if !tc.wantErr && math.Abs(got-tc.wantMeters) > tc.toleranceM {
				t.Fatalf("distance = %.2f, want %.2f +/- %.2f", got, tc.wantMeters, tc.toleranceM)
			}
		})
	}
}

func atVenue() *VenueCentre {
	return &VenueCentre{Latitude: 0, Longitude: 0, RadiusMeters: 50}
}

// fixAt builds a position `degrees` of longitude east of the venue centre.
// 0.001 degrees at the equator is about 111 metres.
func fixAt(degrees, accuracy float64) *Fix {
	return &Fix{Latitude: 0, Longitude: degrees, AccuracyMeters: accuracy}
}

func TestValidationOrderAndCodes(t *testing.T) {
	t.Parallel()
	base := Input{
		SessionOpen: true, CodeValid: true, OnRoster: true,
		Fix: fixAt(0, 5), Venue: atVenue(), Now: time.Unix(100, 0),
		StartedAt: time.Unix(0, 0), LateAfter: time.Minute,
	}
	tests := []struct {
		name  string
		edit  func(*Input)
		error ErrorCode
	}{
		{"closed session before code", func(i *Input) { i.SessionOpen = false; i.CodeValid = false }, ErrSessionClosed},
		{"wrong code before roster", func(i *Input) { i.CodeValid = false; i.OnRoster = false }, ErrWrongCode},
		{"missing GPS fix", func(i *Input) { i.Fix = nil }, ErrLocationRequired},
		{"not on roster", func(i *Input) { i.OnRoster = false }, ErrNotOnRoster},
		{"duplicate before device", func(i *Input) { i.AlreadyChecked = true; i.DeviceUsed = true }, ErrAlreadyExists},
		{"device already used", func(i *Input) { i.DeviceUsed = true }, ErrDeviceAlreadyUsed},
		{"invalid accuracy", func(i *Input) { i.Fix = fixAt(0, -1) }, ErrInvalidInput},
		{"invalid coordinate", func(i *Input) { i.Fix = &Fix{Latitude: 91} }, ErrInvalidInput},
		{"invalid radius", func(i *Input) { i.Venue = &VenueCentre{RadiusMeters: -1} }, ErrInvalidInput},
		{"invalid roster policy", func(i *Input) { i.RosterPolicy = "bad" }, ErrInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := base
			tc.edit(&input)
			_, got := Validate(input)
			if got != tc.error {
				t.Fatalf("Validate() error = %q, want %q", got, tc.error)
			}
		})
	}
}

// Location is required as evidence but distance is never a gate. However far
// out or however vague the fix, only the unverified flag changes.
func TestValidationNeverRefusesOnLocation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		fix         *Fix
		venue       *VenueCentre
		unverified  bool
		hasDistance bool
	}{
		{"inside the radius", fixAt(0, 5), atVenue(), false, true},
		{"outside but the accuracy explains it", fixAt(0.001, 80), atVenue(), false, true},
		{"outside and the accuracy cannot explain it", fixAt(0.001, 5), atVenue(), true, true},
		{"absurd accuracy cannot buy unlimited slack", fixAt(0.01, 100_000), atVenue(), true, true},
		{"venue has no coordinates", fixAt(0.5, 5), nil, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, code := Validate(Input{
				SessionOpen: true, CodeValid: true, OnRoster: true,
				Fix: tc.fix, Venue: tc.venue,
				Now: time.Unix(0, 0), StartedAt: time.Unix(0, 0),
			})
			if code != "" {
				t.Fatalf("Validate() refused on location with %q", code)
			}
			if result.LocationUnverified != tc.unverified {
				t.Fatalf("LocationUnverified = %v, want %v", result.LocationUnverified, tc.unverified)
			}
			if (result.DistanceMeters != nil) != tc.hasDistance {
				t.Fatalf("DistanceMeters = %v, want present=%v", result.DistanceMeters, tc.hasDistance)
			}
		})
	}
}

// A student whose phone reports a vague fix is not penalised for it: the
// accuracy only widens the tolerance, and the advisory flag is separate from
// the decision.
func TestValidationAccuracyIsAdvisoryAndLateBoundary(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_700_000_000, 0)
	input := Input{
		SessionOpen: true, CodeValid: true, OnRoster: true,
		Fix: fixAt(0, 101), Venue: atVenue(), Now: start.Add(5 * time.Minute),
		StartedAt: start, LateAfter: 5 * time.Minute,
	}
	result, code := Validate(input)
	if code != "" || !result.AccuracyPoor || result.Late || result.LocationUnverified {
		t.Fatalf("boundary result = %+v, error=%q", result, code)
	}
	input.Now = input.Now.Add(time.Nanosecond)
	if result, code = Validate(input); code != "" || !result.Late {
		t.Fatalf("late result = %+v, error=%q", result, code)
	}
	input.Now = start
	input.Fix = fixAt(0, 100)
	if result, code = Validate(input); code != "" || result.AccuracyPoor {
		t.Fatalf("100m accuracy should be advisory only: result=%+v error=%q", result, code)
	}
	input.OnRoster = false
	input.RosterPolicy = RosterOpen
	if result, code = Validate(input); code != "" || result.OnRoster {
		t.Fatalf("open roster did not accept and flag unregistered check-in: result=%+v error=%q", result, code)
	}
}

func TestAccuracyAllowanceMeters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		accuracy float64
		want     float64
	}{
		{0, 0},
		{-5, 0},
		{40, 40},
		{MaxAccuracyAllowanceMeters, MaxAccuracyAllowanceMeters},
		{5000, MaxAccuracyAllowanceMeters},
	}
	for _, tc := range tests {
		if got := AccuracyAllowanceMeters(tc.accuracy); got != tc.want {
			t.Fatalf("AccuracyAllowanceMeters(%v) = %v, want %v", tc.accuracy, got, tc.want)
		}
	}
}
