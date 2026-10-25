package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"attendance-api/internal/auth"
	"attendance-api/internal/db"
	"attendance-api/internal/httpx"
	"attendance-api/internal/reports"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *reports.Service
}

func NewHandler(service *reports.Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("report service is required")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Get("/sessions/{id}/live", h.liveSession)
	router.Get("/sessions/{id}/report", h.sessionReport)
	router.Get("/sessions/{id}/roster-sheet", h.rosterSheet)
	router.Post("/sessions/{id}/overrides/bulk", h.bulkOverride)
}

func (h *Handler) liveSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := parseSessionID(w, r)
	if !ok {
		return
	}
	filter, ok := parseFilter(w, r)
	if !ok {
		return
	}
	page, err := h.service.LiveSession(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID, filter)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) sessionReport(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := parseSessionID(w, r)
	if !ok {
		return
	}
	filter, ok := parseFilter(w, r)
	if !ok {
		return
	}
	page, err := h.service.SessionReport(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID, filter)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	switch r.URL.Query().Get("format") {
	case "pdf":
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="session-report.pdf"`)
		if err := reports.WriteReportPDF(w, page); err != nil {
			slog.ErrorContext(r.Context(), "write attendance report PDF", "error", err)
		}
		return
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="session-report.csv"`)
		if err := reports.WriteCSV(w, page); err != nil {
			slog.ErrorContext(r.Context(), "write attendance report CSV", "error", err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) rosterSheet(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := parseSessionID(w, r)
	if !ok {
		return
	}
	filter, ok := parseFilter(w, r)
	if !ok {
		return
	}
	page, err := h.service.RosterSheet(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID, filter)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	switch r.URL.Query().Get("format") {
	case "pdf":
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="session-roster.pdf"`)
		if err := reports.WriteRosterPDF(w, page); err != nil {
			slog.ErrorContext(r.Context(), "write roster sheet PDF", "error", err)
		}
		return
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="session-roster.csv"`)
		if err := reports.WriteRosterCSV(w, page); err != nil {
			slog.ErrorContext(r.Context(), "write roster sheet CSV", "error", err)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) bulkOverride(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := parseSessionID(w, r)
	if !ok {
		return
	}
	var request reports.OverrideInput
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	for _, studentID := range request.StudentIDs {
		if _, err := db.ParseUUID(studentID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "A student ID is invalid", nil)
			return
		}
	}
	results, err := h.service.BulkOverride(r.Context(), principal.Identity.InstitutionID,
		sessionID, principal.Identity.UserID, request)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

func requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return auth.Principal{}, false
	}
	return principal, true
}

func parseSessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(id); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Session ID is invalid", nil)
		return "", false
	}
	return id, true
}

func parseFilter(w http.ResponseWriter, r *http.Request) (reports.ReportFilter, bool) {
	filter := reports.ReportFilter{
		DepartmentID: strings.TrimSpace(r.URL.Query().Get("department_id")),
		Limit:        50,
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer", nil)
			return reports.ReportFilter{}, false
		}
		filter.Limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "offset must be an integer", nil)
			return reports.ReportFilter{}, false
		}
		filter.Offset = value
	}
	if filter.DepartmentID != "" {
		if _, err := db.ParseUUID(filter.DepartmentID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "department_id must be a UUID", nil)
			return reports.ReportFilter{}, false
		}
	}
	format := r.URL.Query().Get("format")
	if format != "" && format != "json" && format != "csv" && format != "pdf" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "format must be json, csv, or pdf", nil)
		return reports.ReportFilter{}, false
	}
	if filter.Limit < 1 || filter.Limit > 200 || filter.Offset < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be 1..200 and offset non-negative", nil)
		return reports.ReportFilter{}, false
	}
	return filter, true
}

func writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, reports.ErrSessionNotFound):
		httpx.WriteError(w, http.StatusNotFound, "session_not_found", "Session was not found", nil)
	case errors.Is(err, reports.ErrReportForbidden):
		httpx.WriteError(w, http.StatusForbidden, "session_access_denied", "You cannot view this session", nil)
	case errors.Is(err, reports.ErrInvalidScope):
		httpx.WriteError(w, http.StatusForbidden, "department_scope_denied", "Requested department is outside your scope", nil)
	case errors.Is(err, reports.ErrInvalidOverride):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_override", "Override fields are invalid", nil)
	case errors.Is(err, reports.ErrSessionOpen):
		httpx.WriteError(w, http.StatusConflict, "session_open", "Attendance overrides require a closed session", nil)
	default:
		slog.ErrorContext(r.Context(), "attendance report request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to complete attendance report request", nil)
	}
}
