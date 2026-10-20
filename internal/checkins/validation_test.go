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

func TestValidationOrderAndCodes(t *testing.T) {
	t.Parallel()
	base := Input{
		SessionOpen: true, CodeValid: true, OnRoster: true,
		Latitude: 0, Longitude: 0, VenueLatitude: 0, VenueLongitude: 0,
		RadiusMeters: 50, AccuracyMeters: 5, Now: time.Unix(100, 0),
		StartedAt: time.Unix(0, 0), LateAfter: time.Minute,
	}
	tests := []struct {
		name  string
		edit  func(*Input)
		error ErrorCode
	}{
		{"closed session before code", func(i *Input) { i.SessionOpen = false; i.CodeValid = false }, ErrSessionClosed},
		{"wrong code before roster", func(i *Input) { i.CodeValid = false; i.OnRoster = false }, ErrWrongCode},
		{"not on roster", func(i *Input) { i.OnRoster = false }, ErrNotOnRoster},
		{"outside radius before duplicate", func(i *Input) {
			i.Longitude = 0.01
			i.RadiusMeters = 10
			i.AlreadyChecked = true
		}, ErrOutsideRadius},
		{"duplicate", func(i *Input) { i.AlreadyChecked = true }, ErrAlreadyExists},
		{"invalid location", func(i *Input) { i.Latitude = 91 }, ErrInvalidInput},
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

func TestValidationRadiusAccuracyAndLateBoundary(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_700_000_000, 0)
	input := Input{
		SessionOpen: true, CodeValid: true, OnRoster: true,
		Latitude: 0, Longitude: 0, VenueLatitude: 0, VenueLongitude: 0,
		RadiusMeters: 0, AccuracyMeters: 101, Now: start.Add(5 * time.Minute),
		StartedAt: start, LateAfter: 5 * time.Minute,
	}
	result, code := Validate(input)
	if code != "" || result.DistanceMeters != 0 || !result.AccuracyPoor || result.Late {
		t.Fatalf("boundary result = %+v, error=%q", result, code)
	}
	input.Now = input.Now.Add(time.Nanosecond)
	result, code = Validate(input)
	if code != "" || !result.Late {
		t.Fatalf("late result = %+v, error=%q", result, code)
	}
	input.Now = start
	input.AccuracyMeters = 100
	result, code = Validate(input)
	if code != "" || result.AccuracyPoor {
		t.Fatalf("100m accuracy should be advisory only: result=%+v error=%q", result, code)
	}
	input.OnRoster = false
	input.RosterPolicy = RosterOpen
	result, code = Validate(input)
	if code != "" || result.OnRoster {
		t.Fatalf("open roster did not accept and flag unregistered check-in: result=%+v error=%q", result, code)
	}
	input.RosterPolicy = RosterRegisteredOnly
	input.OnRoster = true
	input.RadiusMeters = -1
	if _, code = Validate(input); code != ErrInvalidInput {
		t.Fatalf("negative radius error = %q", code)
	}
	input.RadiusMeters = 0
	input.AccuracyMeters = -1
	if _, code = Validate(input); code != ErrInvalidInput {
		t.Fatalf("negative accuracy error = %q", code)
	}
}
