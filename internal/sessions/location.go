package sessions

import (
	"errors"
	"math"

	"attendance-api/internal/catalogue"
	"attendance-api/internal/geo"
)

const (
	// MaxDeviceFixAccuracyMeters is the vaguest fix accepted from the
	// lecturer's device. Beyond it the reading says too little about which room
	// they are in to be worth centring a class on.
	MaxDeviceFixAccuracyMeters = 100
	// MinRecentreMeters is the smallest move worth writing. Consumer GPS
	// wanders a few metres while standing still, so below this the fix agrees
	// with the stored centre and nothing is written.
	MinRecentreMeters = 10
)

// LocationSource records where a session's geofence centre came from.
type LocationSource string

const (
	LocationFromDevice LocationSource = "device"
	LocationFromVenue  LocationSource = "venue"
	// LocationNone is used when neither the lecturer's device nor the venue
	// offered a centre. The session opens without a geofence, which is valid:
	// location is advisory evidence and never refuses a check-in.
	LocationNone LocationSource = "none"
)

// LocationOutcome is what an in-session fix from the lecturer's device was
// allowed to do to the geofence.
type LocationOutcome string

const (
	LocationRefined   LocationOutcome = "refined"
	LocationUnchanged LocationOutcome = "unchanged"
	// LocationEstablished is the answer when the session had no centre at all
	// and a trusted fix supplied the first one.
	LocationEstablished      LocationOutcome = "established"
	LocationRejectedAccuracy LocationOutcome = "rejected_accuracy"
	LocationRejectedDistance LocationOutcome = "rejected_distance"
)

// Centre is the geofence a session is running with: a point and the radius
// around it that check-ins are measured against.
type Centre struct {
	Latitude     float64
	Longitude    float64
	RadiusMeters float64
}

var ErrInvalidLocation = errors.New("location fix is invalid")

// DeviceFix is a position reported by the lecturer's phone or browser.
type DeviceFix struct {
	Latitude  float64
	Longitude float64
	// AccuracyMeters is the device's own reported error for this fix.
	AccuracyMeters float64
	// HasAccuracy separates a device fix, which reports its own error and may
	// therefore calibrate the venue, from a bare coordinate override sent
	// without one, which is trusted for this session only.
	HasAccuracy bool
	// Present is false when the caller sent no coordinates at all.
	Present bool
}

// Trusted reports whether a fix is precise enough to write back to the venue.
func (f DeviceFix) Trusted() bool {
	return f.Present && f.HasAccuracy &&
		f.AccuracyMeters >= 0 && f.AccuracyMeters <= MaxDeviceFixAccuracyMeters &&
		geo.ValidCoordinate(f.Latitude, f.Longitude)
}

// RadiusForNewCentre returns the radius to store with a centre captured from a
// device fix, for a venue that has none of its own yet.
//
// It defers to the catalogue's rule rather than repeating it, so a room
// calibrated by a starting class and one calibrated from the venue screen end
// up with the same geofence. The catalogue owns venues; this keeps a single
// definition of what a fresh venue radius is.
func RadiusForNewCentre(accuracyMeters float64) float64 {
	return float64(catalogue.VenueRadiusFromAccuracy(accuracyMeters))
}

// ResolvedLocation is the centre a session will open with.
type ResolvedLocation struct {
	Latitude  float64
	Longitude float64
	Source    LocationSource
	// Calibrates is true when the fix should also be written back to the venue,
	// so the next class in that room starts from a good anchor.
	Calibrates bool
	// Present is false when there is no centre at all: no usable device fix,
	// and a venue that has never been given coordinates. The session then has
	// no geofence and check-ins at it carry no distance.
	Present bool
}

// ResolveStartLocation decides where a starting session's geofence sits.
//
// An administrator never visits the room, so the lecturer's own device is the
// only reliable source of a venue's coordinates. A fix that reports its
// accuracy and is precise enough both centres the session and calibrates the
// venue. A fix too vague to trust is discarded in favour of whatever the venue
// already has, because centring a class on a 500-metre reading would strand the
// students in it. Coordinates sent without an accuracy stay a manual override:
// honoured for this session, never written back to the venue.
func ResolveStartLocation(fix DeviceFix, venue VenueSettings) ResolvedLocation {
	fromVenue := ResolvedLocation{Source: LocationNone}
	if venue.Present {
		fromVenue = ResolvedLocation{
			Latitude: venue.Latitude, Longitude: venue.Longitude,
			Source: LocationFromVenue, Present: true,
		}
	}
	if !fix.Present || !geo.ValidCoordinate(fix.Latitude, fix.Longitude) {
		return fromVenue
	}
	if !fix.HasAccuracy {
		return ResolvedLocation{
			Latitude: fix.Latitude, Longitude: fix.Longitude,
			Source: LocationFromDevice, Present: true,
		}
	}
	if !fix.Trusted() {
		return fromVenue
	}
	return ResolvedLocation{
		Latitude: fix.Latitude, Longitude: fix.Longitude,
		Source: LocationFromDevice, Calibrates: true, Present: true,
	}
}

// ClassifyLocationUpdate decides what a mid-session fix may do to the
// geofence, and returns how far it sits from the current centre.
//
// The centre deliberately does not follow the lecturer out of the room. A fix
// beyond the session's own radius is either a lecturer who stepped out or a
// venue whose stored centre is wrong, and nothing in the reading tells the two
// apart. Moving the geofence on that guess would push every student still in
// the room outside it and pull whoever is near the lecturer in, so the fix is
// reported as drift and the centre is left alone. A lecturer who really is in
// the right room can recalibrate the venue explicitly.
//
// A nil centre is the one case where a fix may move the geofence freely. The
// venue has never been given coordinates, so there is no circle to push anyone
// out of and nothing to contradict: the first trusted fix establishes it, and
// the room is calibrated for every class that follows. Without this a venue
// whose lecturer denied location permission at start could never acquire a
// centre, which is the whole point of capturing it on their device.
func ClassifyLocationUpdate(centre *Centre, fix DeviceFix) (LocationOutcome, float64, error) {
	if !fix.Present || !fix.HasAccuracy || fix.AccuracyMeters < 0 ||
		math.IsNaN(fix.AccuracyMeters) || math.IsInf(fix.AccuracyMeters, 0) ||
		!geo.ValidCoordinate(fix.Latitude, fix.Longitude) {
		return "", 0, ErrInvalidLocation
	}
	if fix.AccuracyMeters > MaxDeviceFixAccuracyMeters {
		return LocationRejectedAccuracy, 0, nil
	}
	if centre == nil {
		return LocationEstablished, 0, nil
	}
	if !geo.ValidCoordinate(centre.Latitude, centre.Longitude) {
		return "", 0, ErrInvalidLocation
	}
	distance, err := geo.DistanceMeters(fix.Latitude, fix.Longitude, centre.Latitude, centre.Longitude)
	if err != nil {
		return "", 0, ErrInvalidLocation
	}
	switch {
	case distance > centre.RadiusMeters:
		return LocationRejectedDistance, distance, nil
	case distance < MinRecentreMeters:
		return LocationUnchanged, distance, nil
	default:
		return LocationRefined, distance, nil
	}
}

// validOptionalCentre checks a session centre that may be absent. All three
// values must be supplied together or all omitted: a latitude without a radius
// describes no circle, and silently defaulting one of them would invent a
// geofence the caller never asked for.
func validOptionalCentre(latitude, longitude, radiusMeters *float64) bool {
	supplied := 0
	for _, value := range []*float64{latitude, longitude, radiusMeters} {
		if value != nil {
			supplied++
		}
	}
	if supplied == 0 {
		return true
	}
	if supplied != 3 {
		return false
	}
	if !geo.ValidCoordinate(*latitude, *longitude) {
		return false
	}
	radius := *radiusMeters
	return !math.IsNaN(radius) && !math.IsInf(radius, 0) &&
		radius > 0 && radius == math.Trunc(radius) && radius <= math.MaxInt32
}
