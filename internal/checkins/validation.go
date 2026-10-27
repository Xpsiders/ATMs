package checkins

import (
	"errors"
	"math"
	"time"
)

const (
	EarthRadiusMeters = 6_371_000
	MaxGoodAccuracyM  = 100
	// MaxAccuracyAllowanceMeters caps how much of a phone's reported GPS error
	// may be added to the venue radius before a fix is treated as implausible.
	// A cheap or indoor-bound handset often reports a fix tens or hundreds of
	// metres off, so holding it to the bare radius punishes the student for
	// their hardware.
	//
	// The session report derives the same flag in SQL and must use the same
	// number; see the location_unverified column in db/queries/identity.sql.
	MaxAccuracyAllowanceMeters = 250
)

type ErrorCode string

const (
	ErrSessionClosed     ErrorCode = "session_closed"
	ErrWrongCode         ErrorCode = "wrong_or_expired_code"
	ErrNotOnRoster       ErrorCode = "not_on_roster"
	ErrAlreadyExists     ErrorCode = "already_checked_in"
	ErrDeviceAlreadyUsed ErrorCode = "device_already_used"
	ErrInvalidInput      ErrorCode = "invalid_checkin_location"
)

type RosterPolicy string

const (
	RosterRegisteredOnly RosterPolicy = "registered_only"
	RosterOpen           RosterPolicy = "open"
)

// Fix is a position reported by the student's own device. A check-in may carry
// none: the browser can refuse permission, or the handset can fail to get a
// lock indoors. A missing fix is never a reason to refuse attendance, so this
// is a pointer rather than three zero-valued floats, which would be
// indistinguishable from a genuine position off the coast of Ghana.
type Fix struct {
	Latitude       float64
	Longitude      float64
	AccuracyMeters float64
}

// VenueCentre is a venue's stored position. A venue may have none, in which
// case there is nothing to measure a check-in against and location simply does
// not contribute to the decision.
type VenueCentre struct {
	Latitude     float64
	Longitude    float64
	RadiusMeters float64
}

type Input struct {
	SessionOpen    bool
	CodeValid      bool
	OnRoster       bool
	RosterPolicy   RosterPolicy
	Fix            *Fix
	Venue          *VenueCentre
	AlreadyChecked bool
	DeviceUsed     bool
	Now            time.Time
	StartedAt      time.Time
	LateAfter      time.Duration
}

type Result struct {
	// DistanceMeters is nil when there was no fix, no venue centre, or both,
	// so nothing could be measured.
	DistanceMeters *float64
	AccuracyPoor   bool
	// LocationUnverified is set when a position was compared against the venue
	// and landed further out than the phone's own reported error can explain.
	// The check-in is still recorded and reported as web_only_unverified
	// rather than refused.
	LocationUnverified bool
	Late               bool
	OnRoster           bool
}

// AccuracyAllowanceMeters returns how far beyond the venue radius a fix with
// the given reported accuracy may land before it stops being plausible.
func AccuracyAllowanceMeters(accuracyMeters float64) float64 {
	if accuracyMeters <= 0 {
		return 0
	}
	return math.Min(accuracyMeters, MaxAccuracyAllowanceMeters)
}

func HaversineMeters(lat1, lon1, lat2, lon2 float64) (float64, error) {
	if !validCoordinate(lat1, lon1) || !validCoordinate(lat2, lon2) {
		return 0, errors.New("latitude or longitude is outside its valid range")
	}
	toRadians := math.Pi / 180
	phi1, phi2 := lat1*toRadians, lat2*toRadians
	dPhi := (lat2 - lat1) * toRadians
	dLambda := (lon2 - lon1) * toRadians
	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	// Floating point roundoff can put a just outside [0, 1].
	a = math.Max(0, math.Min(1, a))
	return 2 * EarthRadiusMeters * math.Asin(math.Sqrt(a)), nil
}

// Validate decides a check-in. Presence is proved by the session being open,
// the rotating code being valid, and the device not already being bound to
// another student in this session. Location contributes evidence only: it can
// mark a check-in unverified for the lecturer, but it can never refuse one.
func Validate(input Input) (Result, ErrorCode) {
	if !input.SessionOpen {
		return Result{}, ErrSessionClosed
	}
	if !input.CodeValid {
		return Result{}, ErrWrongCode
	}
	policy := input.RosterPolicy
	if policy == "" {
		policy = RosterRegisteredOnly
	}
	if policy != RosterRegisteredOnly && policy != RosterOpen {
		return Result{}, ErrInvalidInput
	}
	if !input.OnRoster && policy == RosterRegisteredOnly {
		return Result{}, ErrNotOnRoster
	}
	if input.Fix != nil &&
		(math.IsNaN(input.Fix.AccuracyMeters) || math.IsInf(input.Fix.AccuracyMeters, 0) ||
			input.Fix.AccuracyMeters < 0) {
		return Result{}, ErrInvalidInput
	}
	if input.Venue != nil &&
		(math.IsNaN(input.Venue.RadiusMeters) || math.IsInf(input.Venue.RadiusMeters, 0) ||
			input.Venue.RadiusMeters < 0) {
		return Result{}, ErrInvalidInput
	}

	result := Result{OnRoster: input.OnRoster}
	if input.Fix != nil {
		result.AccuracyPoor = input.Fix.AccuracyMeters > MaxGoodAccuracyM
	}
	if input.Fix != nil && input.Venue != nil {
		distance, err := HaversineMeters(
			input.Fix.Latitude, input.Fix.Longitude,
			input.Venue.Latitude, input.Venue.Longitude)
		if err != nil {
			return Result{}, ErrInvalidInput
		}
		result.DistanceMeters = &distance
		allowance := AccuracyAllowanceMeters(input.Fix.AccuracyMeters)
		if distance > input.Venue.RadiusMeters+allowance {
			result.LocationUnverified = true
		}
	}

	if input.AlreadyChecked {
		return result, ErrAlreadyExists
	}
	if input.DeviceUsed {
		return result, ErrDeviceAlreadyUsed
	}
	result.Late = input.Now.After(input.StartedAt.Add(input.LateAfter))
	return result, ""
}

func validCoordinate(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsInf(latitude, 0) &&
		!math.IsNaN(longitude) && !math.IsInf(longitude, 0) &&
		latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}
