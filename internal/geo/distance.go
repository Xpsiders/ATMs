// Package geo holds the earth-distance math shared by check-in validation and
// session geofencing. It lives in its own package because check-ins already
// import sessions, so the two cannot borrow the function from each other.
package geo

import (
	"errors"
	"math"
)

const EarthRadiusMeters = 6_371_000

// DistanceMeters returns the great-circle distance between two coordinates.
func DistanceMeters(lat1, lon1, lat2, lon2 float64) (float64, error) {
	if !ValidCoordinate(lat1, lon1) || !ValidCoordinate(lat2, lon2) {
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

// ValidCoordinate reports whether a latitude/longitude pair is finite and in
// range.
func ValidCoordinate(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsInf(latitude, 0) &&
		!math.IsNaN(longitude) && !math.IsInf(longitude, 0) &&
		latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}
