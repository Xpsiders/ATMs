package checkins

import (
	"errors"
	"math"
	"time"
)

const (
	EarthRadiusMeters = 6_371_000
	MaxGoodAccuracyM  = 100
)

type ErrorCode string

const (
	ErrSessionClosed ErrorCode = "session_closed"
	ErrWrongCode     ErrorCode = "wrong_or_expired_code"
	ErrNotOnRoster   ErrorCode = "not_on_roster"
	ErrOutsideRadius ErrorCode = "outside_radius"
	ErrAlreadyExists ErrorCode = "already_checked_in"
	ErrInvalidInput  ErrorCode = "invalid_checkin_location"
)

type RosterPolicy string

const (
	RosterRegisteredOnly RosterPolicy = "registered_only"
	RosterOpen           RosterPolicy = "open"
)

type Input struct {
	SessionOpen    bool
	CodeValid      bool
	OnRoster       bool
	RosterPolicy   RosterPolicy
	Latitude       float64
	Longitude      float64
	VenueLatitude  float64
	VenueLongitude float64
	RadiusMeters   float64
	AccuracyMeters float64
	AlreadyChecked bool
	Now            time.Time
	StartedAt      time.Time
	LateAfter      time.Duration
}

type Result struct {
	DistanceMeters float64
	AccuracyPoor   bool
	Late           bool
	OnRoster       bool
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
	if input.RadiusMeters < 0 || math.IsNaN(input.RadiusMeters) ||
		math.IsInf(input.RadiusMeters, 0) ||
		math.IsNaN(input.AccuracyMeters) || math.IsInf(input.AccuracyMeters, 0) ||
		input.AccuracyMeters < 0 {
		return Result{}, ErrInvalidInput
	}
	distance, err := HaversineMeters(input.Latitude, input.Longitude, input.VenueLatitude, input.VenueLongitude)
	if err != nil {
		return Result{}, ErrInvalidInput
	}
	result := Result{
		DistanceMeters: distance,
		AccuracyPoor:   input.AccuracyMeters > MaxGoodAccuracyM,
		OnRoster:       input.OnRoster,
	}
	if distance > input.RadiusMeters {
		return result, ErrOutsideRadius
	}
	if input.AlreadyChecked {
		return result, ErrAlreadyExists
	}
	result.Late = input.Now.After(input.StartedAt.Add(input.LateAfter))
	return result, ""
}

func validCoordinate(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsInf(latitude, 0) &&
		!math.IsNaN(longitude) && !math.IsInf(longitude, 0) &&
		latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}
