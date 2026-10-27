package sessions_test

import (
	"errors"
	"testing"

	"attendance-api/internal/sessions"
)

// A venue near the Lagos mainland, used as a stable anchor for the distance
// cases below.
const (
	venueLatitude  = 6.5244
	venueLongitude = 3.3792
)

func deviceFix(latitude, longitude, accuracy float64) sessions.DeviceFix {
	return sessions.DeviceFix{
		Latitude: latitude, Longitude: longitude,
		AccuracyMeters: accuracy, HasAccuracy: true, Present: true,
	}
}

// offsetLongitude moves east by roughly the given number of metres. One degree
// of longitude is about 111_320 m at the equator, narrowed by the cosine of
// the latitude, which at 6.5 degrees is close enough to 1 for a test fixture.
func offsetLongitude(metres float64) float64 {
	return venueLongitude + metres/110_570
}

func TestResolveStartLocationPrefersTrustedDeviceFix(t *testing.T) {
	venue := sessions.VenueSettings{
		Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75, Present: true,
	}
	resolved := sessions.ResolveStartLocation(deviceFix(6.6, 3.4, 12), venue)
	if resolved.Source != sessions.LocationFromDevice {
		t.Fatalf("source = %q, want device", resolved.Source)
	}
	if !resolved.Calibrates {
		t.Fatal("a trusted fix must calibrate the venue, or the room never gets corrected")
	}
	if resolved.Latitude != 6.6 || resolved.Longitude != 3.4 {
		t.Fatalf("centre = %v,%v, want the device fix", resolved.Latitude, resolved.Longitude)
	}
}

func TestResolveStartLocationFallsBackToVenueOnVagueFix(t *testing.T) {
	venue := sessions.VenueSettings{
		Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75, Present: true,
	}
	resolved := sessions.ResolveStartLocation(
		deviceFix(6.6, 3.4, sessions.MaxDeviceFixAccuracyMeters+1), venue)
	if resolved.Source != sessions.LocationFromVenue {
		t.Fatalf("source = %q, want venue", resolved.Source)
	}
	if resolved.Calibrates {
		t.Fatal("a fix too vague to centre a class must not be written to the venue")
	}
	if resolved.Latitude != venueLatitude {
		t.Fatalf("latitude = %v, want the venue's own", resolved.Latitude)
	}
}

func TestResolveStartLocationWithoutFixOrVenueHasNoCentre(t *testing.T) {
	resolved := sessions.ResolveStartLocation(sessions.DeviceFix{}, sessions.VenueSettings{})
	if resolved.Present {
		t.Fatal("no fix and an uncalibrated venue must leave the session without a geofence")
	}
	if resolved.Source != sessions.LocationNone {
		t.Fatalf("source = %q, want none", resolved.Source)
	}
}

func TestResolveStartLocationTreatsFixWithoutAccuracyAsManualOverride(t *testing.T) {
	venue := sessions.VenueSettings{
		Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75, Present: true,
	}
	resolved := sessions.ResolveStartLocation(
		sessions.DeviceFix{Latitude: 6.7, Longitude: 3.5, Present: true}, venue)
	if resolved.Source != sessions.LocationFromDevice || !resolved.Present {
		t.Fatalf("source = %q, want the supplied coordinates to be honoured", resolved.Source)
	}
	if resolved.Calibrates {
		t.Fatal("coordinates sent without an accuracy must not be written back to the venue")
	}
}

func TestClassifyLocationUpdate(t *testing.T) {
	centre := &sessions.Centre{
		Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75,
	}
	cases := []struct {
		name    string
		centre  *sessions.Centre
		fix     sessions.DeviceFix
		outcome sessions.LocationOutcome
	}{
		{
			name:    "a fix agreeing with the centre changes nothing",
			centre:  centre,
			fix:     deviceFix(venueLatitude, offsetLongitude(4), 10),
			outcome: sessions.LocationUnchanged,
		},
		{
			name:    "a fix inside the radius refines the centre",
			centre:  centre,
			fix:     deviceFix(venueLatitude, offsetLongitude(40), 10),
			outcome: sessions.LocationRefined,
		},
		{
			name:    "a fix outside the radius is drift and must not move the centre",
			centre:  centre,
			fix:     deviceFix(venueLatitude, offsetLongitude(400), 10),
			outcome: sessions.LocationRejectedDistance,
		},
		{
			name:    "a vague fix is ignored",
			centre:  centre,
			fix:     deviceFix(venueLatitude, offsetLongitude(40), sessions.MaxDeviceFixAccuracyMeters+1),
			outcome: sessions.LocationRejectedAccuracy,
		},
		{
			name:    "a trusted fix establishes a first centre",
			centre:  nil,
			fix:     deviceFix(venueLatitude, venueLongitude, 10),
			outcome: sessions.LocationEstablished,
		},
		{
			name:    "a vague fix cannot establish a first centre",
			centre:  nil,
			fix:     deviceFix(venueLatitude, venueLongitude, sessions.MaxDeviceFixAccuracyMeters+1),
			outcome: sessions.LocationRejectedAccuracy,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			outcome, moved, err := sessions.ClassifyLocationUpdate(testCase.centre, testCase.fix)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outcome != testCase.outcome {
				t.Fatalf("outcome = %q, want %q (moved %.1f m)", outcome, testCase.outcome, moved)
			}
		})
	}
}

func TestClassifyLocationUpdateRejectsUnusableInput(t *testing.T) {
	centre := &sessions.Centre{Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75}
	cases := map[string]sessions.DeviceFix{
		"no fix at all":        {},
		"no reported accuracy": {Latitude: venueLatitude, Longitude: venueLongitude, Present: true},
		"negative accuracy":    deviceFix(venueLatitude, venueLongitude, -1),
		"impossible latitude":  deviceFix(91, venueLongitude, 10),
	}
	for name, fix := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := sessions.ClassifyLocationUpdate(centre, fix); !errors.Is(err, sessions.ErrInvalidLocation) {
				t.Fatalf("error = %v, want ErrInvalidLocation", err)
			}
		})
	}
}

func TestRadiusForNewCentreWidensWithAccuracy(t *testing.T) {
	tight := sessions.RadiusForNewCentre(5)
	loose := sessions.RadiusForNewCentre(300)
	if tight != 75 {
		t.Fatalf("radius for a precise fix = %v, want the 75 m default", tight)
	}
	if loose <= tight {
		t.Fatalf("radius for a loose fix = %v, want wider than %v", loose, tight)
	}
}
