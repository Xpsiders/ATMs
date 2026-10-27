package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/db"
	"attendance-api/internal/httpx"
	"attendance-api/internal/sessions"

	"github.com/go-chi/chi/v5"
)

type AccessStore interface {
	CanManageOffering(context.Context, string, string, string) (bool, error)
	CanManageSession(context.Context, string, string, string) (bool, error)
	VenueSettings(context.Context, string, string) (sessions.VenueSettings, error)
}

type Handler struct {
	service *sessions.Service
	access  AccessStore
}

func NewHandler(service *sessions.Service, access AccessStore) (*Handler, error) {
	if service == nil || access == nil {
		return nil, errors.New("session service and access store are required")
	}
	return &Handler{service: service, access: access}, nil
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Get("/me/live-classes", h.liveClasses)
	router.Post("/sessions", h.start)
	router.Patch("/sessions/{id}", h.fixCode)
	router.Post("/sessions/{id}/location", h.updateLocation)
	router.Post("/sessions/{id}/scanner-status", h.scannerStatus)
	router.Post("/sessions/{id}/end", h.end)
	router.Get("/sessions/{id}/code", h.code)
}

func (h *Handler) liveClasses(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	if principal.Identity.StudentID == "" {
		httpx.WriteError(w, http.StatusForbidden, "student_account_required", "Only a student account can view live classes", nil)
		return
	}
	items, err := h.service.LiveClasses(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, principal.Identity.StudentID)
	if err != nil {
		slog.ErrorContext(r.Context(), "load current user's live classes failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to load live classes", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) scannerStatus(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	sessionID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(sessionID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Session ID is invalid", nil)
		return
	}
	var request struct {
		State string `json:"state" validate:"required,eq=unavailable"`
		Note  string `json:"note" validate:"required,max=1000"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "state must be unavailable and note is required", nil)
		return
	}
	allowed, err := h.access.CanManageSession(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID)
	if err != nil {
		slog.ErrorContext(r.Context(), "scanner status authorization failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to authorize scanner status", nil)
		return
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "session_access_denied", "You cannot manage this session", nil)
		return
	}
	err = h.service.MarkScannerUnavailable(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID, request.Note)
	switch {
	case errors.Is(err, sessions.ErrSessionNotFound):
		httpx.WriteError(w, http.StatusNotFound, "session_not_found", "Session was not found", nil)
	case errors.Is(err, sessions.ErrSessionEnded):
		httpx.WriteError(w, http.StatusConflict, "session_closed", "Scanner status can only be changed while the session is open", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "mark scanner unavailable failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to mark scanner unavailable", nil)
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"state": "unavailable"})
	}
}

func (h *Handler) fixCode(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	sessionID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(sessionID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Session ID is invalid", nil)
		return
	}
	var request struct {
		Rotating *bool `json:"rotating" validate:"required"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil || *request.Rotating {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "rotating must be false; fixed codes cannot be changed back", nil)
		return
	}
	allowed, err := h.access.CanManageSession(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID)
	if err != nil {
		slog.ErrorContext(r.Context(), "fixed-code authorization failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to authorize code conversion", nil)
		return
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "session_access_denied", "You cannot manage this session", nil)
		return
	}
	result, err := h.service.ConvertToFixedCode(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID)
	switch {
	case errors.Is(err, sessions.ErrSessionNotFound):
		httpx.WriteError(w, http.StatusNotFound, "session_not_found", "Session was not found", nil)
	case errors.Is(err, sessions.ErrSessionEnded):
		httpx.WriteError(w, http.StatusConflict, "session_closed", "Session is closed", nil)
	case errors.Is(err, sessions.ErrCodeAlreadyFixed):
		httpx.WriteError(w, http.StatusConflict, "code_already_fixed", "Session already uses a fixed code", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "fixed-code conversion failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to convert session code", nil)
	default:
		httpx.WriteJSON(w, http.StatusOK, result)
	}
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	var request struct {
		OfferingID   string   `json:"offering_id" validate:"required,uuid"`
		VenueID      string   `json:"venue_id" validate:"required,uuid"`
		TimetableID  string   `json:"timetable_id" validate:"omitempty,uuid"`
		RotatingCode *bool    `json:"rotating_code"`
		LateAfterMin *int     `json:"late_after_min" validate:"omitempty,gte=0,lte=153722867"`
		Latitude     *float64 `json:"latitude" validate:"omitempty,gte=-90,lte=90"`
		Longitude    *float64 `json:"longitude" validate:"omitempty,gte=-180,lte=180"`
		RadiusMeters *int     `json:"radius_meters" validate:"omitempty,gte=1,lte=2147483647"`
		// AccuracyMeters is the lecturer device's own reported error for the
		// fix above. It is what separates a position good enough to calibrate
		// the room from one that would strand the class, so it is sent
		// unmodified by the app rather than rounded or defaulted here.
		AccuracyMeters *float64 `json:"accuracy_meters" validate:"omitempty,gte=0"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Session fields are invalid", nil)
		return
	}
	allowed, err := h.access.CanManageOffering(r.Context(), principal.Identity.InstitutionID,
		request.OfferingID, principal.Identity.UserID)
	if err != nil {
		slog.ErrorContext(r.Context(), "session offering authorization failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to authorize session start", nil)
		return
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "offering_access_denied", "You cannot manage this offering", nil)
		return
	}
	venue, err := h.access.VenueSettings(r.Context(), principal.Identity.InstitutionID, request.VenueID)
	if errors.Is(err, sessions.ErrVenueNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "venue_not_found", "Venue was not found", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "load session venue settings failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to load venue settings", nil)
		return
	}
	rotating := true
	if request.RotatingCode != nil {
		rotating = *request.RotatingCode
	}
	lateAfter := 0
	if request.LateAfterMin != nil {
		lateAfter = *request.LateAfterMin
	}
	// The lecturer's own device is the only reliable source of a venue's
	// coordinates, because an administrator is never in the room. A fix that
	// reports its accuracy and is precise enough centres this session and
	// calibrates the venue; a vague one falls back to whatever the venue
	// already has. Coordinates sent without an accuracy stay a manual
	// override, honoured here but never written back to the venue.
	fix := sessions.DeviceFix{Present: request.Latitude != nil && request.Longitude != nil}
	if fix.Present {
		fix.Latitude, fix.Longitude = *request.Latitude, *request.Longitude
	}
	if request.AccuracyMeters != nil {
		fix.AccuracyMeters, fix.HasAccuracy = *request.AccuracyMeters, true
	}
	resolved := sessions.ResolveStartLocation(fix, venue)
	var latitude, longitude, radius *float64
	if resolved.Present {
		centreLatitude, centreLongitude := resolved.Latitude, resolved.Longitude
		latitude, longitude = &centreLatitude, &centreLongitude
		// A geofence needs a radius. The venue's own radius wins, since it was
		// set for that room; a venue being calibrated for the first time has
		// none, so one is derived from how precise the fix was.
		centreRadius := sessions.RadiusForNewCentre(fix.AccuracyMeters)
		if venue.Present && venue.RadiusMeters > 0 {
			centreRadius = venue.RadiusMeters
		}
		radius = &centreRadius
	}
	// An explicit radius still overrides, but only where there is a centre for
	// it to draw a circle around.
	if request.RadiusMeters != nil && radius != nil {
		requested := float64(*request.RadiusMeters)
		radius = &requested
	}
	started, err := h.service.Start(r.Context(), sessions.StartInput{
		InstitutionID: principal.Identity.InstitutionID, OfferingID: request.OfferingID,
		VenueID: request.VenueID, TimetableID: request.TimetableID, StartedBy: principal.Identity.UserID,
		Latitude: latitude, Longitude: longitude, RadiusMeters: radius, RotatingCode: rotating,
		LateAfter: time.Duration(lateAfter) * time.Minute, LocationSource: resolved.Source,
		CalibrateVenue: resolved.Calibrates, AccuracyMeters: fix.AccuracyMeters,
	})
	var venueErr *sessions.OpenVenueError
	switch {
	case errors.As(err, &venueErr):
		httpx.WriteError(w, http.StatusConflict, "venue_session_already_open",
			"A session is already open at this venue", map[string]any{"session_id": venueErr.SessionID})
		return
	case errors.Is(err, sessions.ErrInvalidSession):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_session", "Session parameters are invalid", nil)
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "session start failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to start session", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, started)
}

// updateLocation applies a fix taken on the lecturer's device while the class
// is running, so a room whose stored centre is wrong, or absent, is corrected
// by the person who is actually standing in it.
func (h *Handler) updateLocation(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	sessionID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(sessionID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Session ID is invalid", nil)
		return
	}
	// Accuracy is required here, unlike on session start. A mid-session fix
	// only ever moves a geofence students are already checking in against, and
	// without the device's own error there is no way to tell a precise
	// correction from a wild reading.
	var request struct {
		Latitude       *float64 `json:"latitude" validate:"required,gte=-90,lte=90"`
		Longitude      *float64 `json:"longitude" validate:"required,gte=-180,lte=180"`
		AccuracyMeters *float64 `json:"accuracy_meters" validate:"required,gte=0"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Location fields are invalid", nil)
		return
	}
	allowed, err := h.access.CanManageSession(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID)
	if err != nil {
		slog.ErrorContext(r.Context(), "session location authorization failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to authorize location update", nil)
		return
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "session_access_denied", "You cannot manage this session", nil)
		return
	}
	result, err := h.service.UpdateLocation(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID, sessions.DeviceFix{
			Latitude: *request.Latitude, Longitude: *request.Longitude,
			AccuracyMeters: *request.AccuracyMeters, HasAccuracy: true, Present: true,
		})
	switch {
	case errors.Is(err, sessions.ErrSessionNotFound):
		httpx.WriteError(w, http.StatusNotFound, "session_not_found", "Session was not found", nil)
		return
	case errors.Is(err, sessions.ErrSessionEnded):
		httpx.WriteError(w, http.StatusConflict, "session_closed", "This session has already ended", nil)
		return
	case errors.Is(err, sessions.ErrInvalidLocation):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Location fields are invalid", nil)
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "session location update failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to update session location", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) end(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	sessionID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(sessionID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Session ID is invalid", nil)
		return
	}
	allowed, err := h.access.CanManageSession(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID)
	if err != nil {
		slog.ErrorContext(r.Context(), "session end authorization failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to authorize session end", nil)
		return
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "session_access_denied", "You cannot manage this session", nil)
		return
	}
	session, err := h.service.End(r.Context(), principal.Identity.InstitutionID, sessionID)
	switch {
	case errors.Is(err, sessions.ErrSessionNotFound):
		httpx.WriteError(w, http.StatusNotFound, "session_not_found", "Session was not found", nil)
	case errors.Is(err, sessions.ErrSessionEnded):
		httpx.WriteError(w, http.StatusConflict, "session_closed", "Session is already closed", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "session end failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to end session", nil)
	default:
		httpx.WriteJSON(w, http.StatusOK, session)
	}
}

func (h *Handler) code(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	sessionID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(sessionID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Session ID is invalid", nil)
		return
	}
	allowed, err := h.access.CanManageSession(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID)
	if err != nil {
		slog.ErrorContext(r.Context(), "session code authorization failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to authorize session code access", nil)
		return
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "session_access_denied", "You cannot manage this session", nil)
		return
	}
	result, err := h.service.Code(r.Context(), principal.Identity.InstitutionID, sessionID)
	switch {
	case errors.Is(err, sessions.ErrSessionNotFound):
		httpx.WriteError(w, http.StatusNotFound, "session_not_found", "Session was not found", nil)
	case errors.Is(err, sessions.ErrSessionEnded):
		httpx.WriteError(w, http.StatusConflict, "session_closed", "Session is closed", nil)
	case errors.Is(err, sessions.ErrCodeExpired):
		httpx.WriteError(w, http.StatusConflict, "code_expired", "Static session code has expired", nil)
	case err != nil:
		slog.ErrorContext(r.Context(), "session code lookup failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to get session code", nil)
	default:
		httpx.WriteJSON(w, http.StatusOK, result)
	}
}
