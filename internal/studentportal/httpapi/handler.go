package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"attendance-api/internal/auth"
	"attendance-api/internal/db"
	"attendance-api/internal/httpx"
	"attendance-api/internal/studentportal"

	"github.com/go-chi/chi/v5"
	_ "time/tzdata"
)

type Handler struct {
	service *studentportal.Service
}

func NewHandler(service *studentportal.Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("student portal service is required")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Get("/me/courses", h.courses)
	router.Get("/me/timetable", h.timetable)
	router.Get("/me/attendance", h.attendance)
	router.Get("/me/registrations", h.pendingRegistrations)
}

func (h *Handler) courses(w http.ResponseWriter, r *http.Request) {
	principal, ok := studentPrincipal(w, r)
	if !ok {
		return
	}
	items, err := h.service.Courses(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, principal.Identity.StudentID)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) timetable(w http.ResponseWriter, r *http.Request) {
	principal, ok := studentPrincipal(w, r)
	if !ok {
		return
	}
	date, items, err := h.service.Timetable(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, principal.Identity.StudentID, r.URL.Query().Get("date"))
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"date": date, "items": items})
}

func (h *Handler) attendance(w http.ResponseWriter, r *http.Request) {
	principal, ok := studentPrincipal(w, r)
	if !ok {
		return
	}
	offeringID := r.URL.Query().Get("offering_id")
	if _, err := db.ParseUUID(offeringID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "offering_id must be a UUID", nil)
		return
	}
	items, err := h.service.Attendance(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, principal.Identity.StudentID, offeringID)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) pendingRegistrations(w http.ResponseWriter, r *http.Request) {
	principal, ok := studentPrincipal(w, r)
	if !ok {
		return
	}
	items, err := h.service.PendingRegistrations(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, principal.Identity.StudentID)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func studentPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return auth.Principal{}, false
	}
	if principal.Identity.StudentID == "" {
		httpx.WriteError(w, http.StatusForbidden, "student_account_required", "Only a claimed student can access this resource", nil)
		return auth.Principal{}, false
	}
	return principal, true
}

func writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, studentportal.ErrNotStudent):
		httpx.WriteError(w, http.StatusForbidden, "student_account_required", "Only a claimed student can access this resource", nil)
	case errors.Is(err, studentportal.ErrInvalidDate):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "date must be a valid YYYY-MM-DD calendar date", nil)
	case errors.Is(err, studentportal.ErrOfferingUnapproved):
		httpx.WriteError(w, http.StatusNotFound, "offering_not_found", "Approved offering was not found", nil)
	default:
		slog.ErrorContext(r.Context(), "student portal request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to load student data", nil)
	}
}
