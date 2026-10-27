package sessions

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"
)

var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrSessionEnded     = errors.New("session is already ended")
	ErrCodeExpired      = errors.New("session code has expired")
	ErrInvalidSession   = errors.New("session parameters are invalid")
	ErrVenueNotFound    = errors.New("venue not found")
	ErrSessionNotOpen   = errors.New("session is not open")
	ErrCodeAlreadyFixed = errors.New("session code is already fixed")
)

type OpenVenueError struct {
	SessionID string
}

func (e *OpenVenueError) Error() string {
	return fmt.Sprintf("venue already has open session %s", e.SessionID)
}

type Session struct {
	ID            string     `json:"id"`
	InstitutionID string     `json:"-"`
	OfferingID    string     `json:"offering_id"`
	VenueID       string     `json:"venue_id"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at"`
	// Latitude, Longitude, and RadiusMeters are nil when the session has no
	// geofence, because neither the lecturer's device nor the venue supplied
	// a centre. Check-ins then carry no distance and are never refused on it.
	Latitude     *float64 `json:"latitude"`
	Longitude    *float64 `json:"longitude"`
	RadiusMeters *float64 `json:"radius_meters"`

	RotatingCode  bool          `json:"rotating_code"`
	StaticCode    string        `json:"-"`
	CodeExpiresAt *time.Time    `json:"-"`
	LateAfter     time.Duration `json:"-"`
	LateAfterMin  int64         `json:"late_after_min"`
}

type StartInput struct {
	InstitutionID string
	OfferingID    string
	VenueID       string
	TimetableID   string
	StartedBy     string
	// Latitude, Longitude, and RadiusMeters are nil when the session opens
	// without a geofence.
	Latitude     *float64
	Longitude    *float64
	RadiusMeters *float64
	RotatingCode bool
	LateAfter    time.Duration
	// LocationSource and CalibrateVenue carry the decision made by
	// ResolveStartLocation: where the centre came from, and whether it is good
	// enough to write back to the venue in the same transaction.
	LocationSource LocationSource
	CalibrateVenue bool
	AccuracyMeters float64
}

type SessionStore interface {
	Create(context.Context, StartInput, time.Time, string, *time.Time) (Session, error)
	Get(context.Context, string, string) (Session, bool, error)
	End(context.Context, string, string, time.Time) (Session, error)
}

type LiveClass struct {
	SessionID        string    `json:"session_id"`
	OfferingID       string    `json:"offering_id"`
	CourseCode       string    `json:"course_code"`
	CourseTitle      string    `json:"course_title"`
	VenueID          string    `json:"venue_id"`
	VenueName        string    `json:"venue_name"`
	LecturerName     string    `json:"lecturer_name"`
	StartedAt        time.Time `json:"started_at"`
	LateAfterMin     int       `json:"late_after_min"`
	RotatingCode     bool      `json:"rotating_code"`
	AlreadyCheckedIn bool      `json:"already_checked_in"`
}

type LiveClassStore interface {
	ListLiveClasses(context.Context, string, string, string) ([]LiveClass, error)
}

type FixedCodeStore interface {
	ConvertToFixedCode(context.Context, string, string, string, string, time.Time) (Session, error)
}

// LocationStore recentres an open session on a fix from the lecturer's device
// and calibrates the venue from the same fix, so the next class in that room
// starts from a good anchor.
type LocationStore interface {
	RecentreSession(context.Context, string, string, string, Centre, DeviceFix, time.Time) error
}

// LocationResult is the answer to a mid-session fix: what the server did with
// it, and the geofence that is in force afterwards.
type LocationResult struct {
	SessionID string          `json:"session_id"`
	Outcome   LocationOutcome `json:"outcome"`
	// Latitude, Longitude, and RadiusMeters are the geofence in force after
	// the call, and are nil when the session still has no centre: the venue
	// has never been calibrated and this fix was too vague to be its first.
	Latitude     *float64 `json:"latitude"`
	Longitude    *float64 `json:"longitude"`
	RadiusMeters *float64 `json:"radius_meters"`
	MovedMeters  float64  `json:"moved_meters"`
}

type ScannerStatusStore interface {
	MarkScannerUnavailable(context.Context, string, string, string, string, time.Time) error
}

type VenueSettings struct {
	Latitude     float64
	Longitude    float64
	RadiusMeters float64
	// Present is false for a venue that has never been given coordinates. A
	// venue is a named room first; its position is optional metadata.
	Present bool
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	store      SessionStore
	clock      Clock
	codeSecret []byte
	codeWindow time.Duration
	staticTTL  time.Duration
	random     io.Reader
	randomMu   sync.Mutex
}

type Started struct {
	Session          Session `json:"session"`
	Code             string  `json:"code"`
	SecondsRemaining int     `json:"seconds_remaining"`
	// LocationSource tells the lecturer's app whether its own fix was used or
	// the venue's stored location was fallen back on.
	LocationSource LocationSource `json:"location_source"`
}

func NewService(store SessionStore, clock Clock, codeSecret []byte, codeWindow, staticTTL time.Duration) (*Service, error) {
	if store == nil || clock == nil {
		return nil, errors.New("session store and clock are required")
	}
	if len(codeSecret) < 32 {
		return nil, errors.New("code HMAC secret must contain at least 32 bytes")
	}
	if codeWindow < time.Second || staticTTL <= 0 {
		return nil, errors.New("code window and static code lifetime must be positive")
	}
	return &Service{
		store: store, clock: clock, codeSecret: append([]byte(nil), codeSecret...),
		codeWindow: codeWindow, staticTTL: staticTTL, random: rand.Reader,
	}, nil
}

func (s *Service) SetRandomSource(source io.Reader) error {
	if source == nil {
		return errors.New("random source is required")
	}
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	s.random = source
	return nil
}

func (s *Service) Start(ctx context.Context, input StartInput) (Started, error) {
	if input.InstitutionID == "" || input.OfferingID == "" || input.VenueID == "" || input.StartedBy == "" {
		return Started{}, errors.New("institution, offering, venue, and actor are required")
	}
	// A session may open with no geofence at all; coordinates are validated
	// only when they are actually supplied.
	if !validOptionalCentre(input.Latitude, input.Longitude, input.RadiusMeters) ||
		input.LateAfter < 0 || input.LateAfter%time.Minute != 0 ||
		input.LateAfter/time.Minute > math.MaxInt32 {
		return Started{}, ErrInvalidSession
	}
	now := s.clock.Now().UTC()
	staticCode := ""
	var expiresAt *time.Time
	if !input.RotatingCode {
		code, err := s.newStaticCode()
		if err != nil {
			return Started{}, err
		}
		staticCode = code
		expiry := now.Add(s.staticTTL)
		expiresAt = &expiry
	}
	session, err := s.store.Create(ctx, input, now, staticCode, expiresAt)
	if err != nil {
		return Started{}, err
	}
	session.LateAfterMin = int64(session.LateAfter / time.Minute)
	// A caller that did not resolve a source still gets an honest one: a
	// session that ended up with no centre reports none rather than claiming
	// the venue supplied one.
	source := input.LocationSource
	if source == "" {
		source = LocationFromVenue
		if sessionCentre(session) == nil {
			source = LocationNone
		}
	}
	result := Started{Session: session, LocationSource: source}
	if session.RotatingCode {
		result.Code, result.SecondsRemaining, err = CurrentCode(s.codeSecret, session.ID, now, s.codeWindow)
		if err != nil {
			return Started{}, fmt.Errorf("derive current session code: %w", err)
		}
	} else {
		result.Code = session.StaticCode
		if session.CodeExpiresAt != nil {
			result.SecondsRemaining = int(session.CodeExpiresAt.Sub(now).Seconds())
		}
	}
	return result, nil
}

func (s *Service) Code(ctx context.Context, institutionID, sessionID string) (Started, error) {
	session, found, err := s.store.Get(ctx, institutionID, sessionID)
	if err != nil {
		return Started{}, fmt.Errorf("load session code: %w", err)
	}
	if !found {
		return Started{}, ErrSessionNotFound
	}
	if session.EndedAt != nil {
		return Started{}, ErrSessionEnded
	}
	now := s.clock.Now().UTC()
	result := Started{Session: session}
	if session.RotatingCode {
		result.Code, result.SecondsRemaining, err = CurrentCode(s.codeSecret, session.ID, now, s.codeWindow)
		if err != nil {
			return Started{}, err
		}
		return result, nil
	}
	if session.CodeExpiresAt != nil && !now.Before(*session.CodeExpiresAt) {
		return Started{}, ErrCodeExpired
	}
	result.Code = session.StaticCode
	if session.CodeExpiresAt != nil {
		result.SecondsRemaining = int(session.CodeExpiresAt.Sub(now).Seconds())
	}
	return result, nil
}

// UpdateLocation applies a mid-session fix from the lecturer's device. A fix
// that agrees with the current centre, or that is too vague or too far to
// trust, leaves the geofence untouched; only a refinement, or the first centre
// for a venue that has none, is written.
//
// A session with no centre takes its radius from RadiusForNewCentre, so a
// venue calibrated mid-class gets the same radius rule as one calibrated from
// the venue screen.
func (s *Service) UpdateLocation(
	ctx context.Context,
	institutionID, sessionID, actorUserID string,
	fix DeviceFix,
) (LocationResult, error) {
	if institutionID == "" || sessionID == "" || actorUserID == "" {
		return LocationResult{}, ErrInvalidLocation
	}
	session, found, err := s.store.Get(ctx, institutionID, sessionID)
	if err != nil {
		return LocationResult{}, fmt.Errorf("load session for location update: %w", err)
	}
	if !found {
		return LocationResult{}, ErrSessionNotFound
	}
	if session.EndedAt != nil {
		return LocationResult{}, ErrSessionEnded
	}
	current := sessionCentre(session)
	outcome, moved, err := ClassifyLocationUpdate(current, fix)
	if err != nil {
		return LocationResult{}, err
	}
	result := LocationResult{SessionID: session.ID, Outcome: outcome, MovedMeters: moved}
	if current != nil {
		result.Latitude, result.Longitude = &current.Latitude, &current.Longitude
		result.RadiusMeters = &current.RadiusMeters
	}
	if outcome != LocationRefined && outcome != LocationEstablished {
		return result, nil
	}
	store, ok := s.store.(LocationStore)
	if !ok {
		return LocationResult{}, errors.New("session location updates are not supported by this store")
	}
	// A refinement keeps the radius the session is already running with; a
	// first centre has none to keep, so one is derived from the fix.
	centre := Centre{
		Latitude: fix.Latitude, Longitude: fix.Longitude,
		RadiusMeters: RadiusForNewCentre(fix.AccuracyMeters),
	}
	if current != nil {
		centre.RadiusMeters = current.RadiusMeters
	}
	if err := store.RecentreSession(ctx, institutionID, session.ID, actorUserID,
		centre, fix, s.clock.Now().UTC()); err != nil {
		return LocationResult{}, fmt.Errorf("recentre session: %w", err)
	}
	result.Latitude, result.Longitude = &centre.Latitude, &centre.Longitude
	result.RadiusMeters = &centre.RadiusMeters
	return result, nil
}

// sessionCentre reads a session's geofence, which is absent when the venue had
// no coordinates to snapshot at start.
func sessionCentre(session Session) *Centre {
	if session.Latitude == nil || session.Longitude == nil || session.RadiusMeters == nil {
		return nil
	}
	return &Centre{
		Latitude: *session.Latitude, Longitude: *session.Longitude,
		RadiusMeters: *session.RadiusMeters,
	}
}

func (s *Service) ConvertToFixedCode(ctx context.Context, institutionID, sessionID, actorUserID string) (Started, error) {
	store, ok := s.store.(FixedCodeStore)
	if !ok {
		return Started{}, errors.New("fixed session codes are not supported by this store")
	}
	session, found, err := s.store.Get(ctx, institutionID, sessionID)
	if err != nil {
		return Started{}, fmt.Errorf("load session for code conversion: %w", err)
	}
	if !found {
		return Started{}, ErrSessionNotFound
	}
	if session.EndedAt != nil {
		return Started{}, ErrSessionEnded
	}
	if !session.RotatingCode {
		return Started{}, ErrCodeAlreadyFixed
	}
	code, err := s.newStaticCode()
	if err != nil {
		return Started{}, err
	}
	session, err = store.ConvertToFixedCode(ctx, institutionID, sessionID, actorUserID, code, s.clock.Now().UTC())
	if err != nil {
		return Started{}, fmt.Errorf("persist fixed session code: %w", err)
	}
	return Started{Session: session, Code: code}, nil
}

func (s *Service) MarkScannerUnavailable(ctx context.Context, institutionID, sessionID, actorUserID, note string) error {
	store, ok := s.store.(ScannerStatusStore)
	if !ok {
		return errors.New("scanner status is not supported by this store")
	}
	if note == "" {
		return errors.New("scanner outage note is required")
	}
	session, found, err := s.store.Get(ctx, institutionID, sessionID)
	if err != nil {
		return fmt.Errorf("load session for scanner status: %w", err)
	}
	if !found {
		return ErrSessionNotFound
	}
	if session.EndedAt != nil {
		return ErrSessionEnded
	}
	if err := store.MarkScannerUnavailable(ctx, institutionID, sessionID, actorUserID, note, s.clock.Now().UTC()); err != nil {
		return fmt.Errorf("mark scanner unavailable: %w", err)
	}
	return nil
}

func (s *Service) End(ctx context.Context, institutionID, sessionID string) (Session, error) {
	session, err := s.store.End(ctx, institutionID, sessionID, s.clock.Now().UTC())
	if err != nil {
		return Session{}, fmt.Errorf("end lecture session: %w", err)
	}
	return session, nil
}

func (s *Service) LiveClasses(ctx context.Context, institutionID, userID, studentID string) ([]LiveClass, error) {
	store, ok := s.store.(LiveClassStore)
	if !ok {
		return nil, errors.New("live classes are not supported by this store")
	}
	if institutionID == "" || userID == "" || studentID == "" {
		return nil, errors.New("institution, user, and student IDs are required")
	}
	items, err := store.ListLiveClasses(ctx, institutionID, userID, studentID)
	if err != nil {
		return nil, fmt.Errorf("list live classes: %w", err)
	}
	if items == nil {
		items = []LiveClass{}
	}
	return items, nil
}

func (s *Service) newStaticCode() (string, error) {
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	const space = uint32(1_000_000)
	const limit = ^uint32(0) - (^uint32(0) % space)
	for {
		var data [4]byte
		if _, err := io.ReadFull(s.random, data[:]); err != nil {
			return "", fmt.Errorf("generate static session code: %w", err)
		}
		value := binary.BigEndian.Uint32(data[:])
		if value < limit {
			return fmt.Sprintf("%06d", value%space), nil
		}
	}
}
