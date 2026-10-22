package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"attendance-api/internal/auth"
	"attendance-api/internal/db"
	"attendance-api/internal/httpx"
	"attendance-api/internal/registration"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *registration.Service
}

func NewHandler(service *registration.Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("registration service is required")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Post("/offerings/{id}/registrations", h.register)
	router.Get("/offerings/{id}/registrations", h.list)
	router.Patch("/offerings/{id}/registrations/{student_id}", h.review)
	router.Post("/offerings/{id}/registrations/bulk", h.bulkReview)
	router.Get("/registration-approvals", h.pendingApprovals)
}

func (h *Handler) pendingApprovals(w http.ResponseWriter, r *http.Request) {
	principal, ok := principal(w, r)
	if !ok {
		return
	}
	limit, valid := queryInt(r, "limit", 50)
	if !valid {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer", nil)
		return
	}
	offset, valid := queryInt(r, "offset", 0)
	if !valid {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "offset must be an integer", nil)
		return
	}
	page, err := h.service.PendingApprovals(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, limit, offset)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) bulkReview(w http.ResponseWriter, r *http.Request) {
	principal, ok := principal(w, r)
	if !ok {
		return
	}
	offeringID, ok := pathUUID(w, chi.URLParam(r, "id"), "Offering")
	if !ok {
		return
	}
	var request struct {
		StudentIDs []string `json:"student_ids"`
		Status     string   `json:"status"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if len(request.StudentIDs) == 0 || len(request.StudentIDs) > 200 ||
		(request.Status != "approved" && request.Status != "rejected") {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "student_ids and status are invalid", nil)
		return
	}
	for _, studentID := range request.StudentIDs {
		if _, err := db.ParseUUID(studentID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "A student ID is invalid", nil)
			return
		}
	}
	items, err := h.service.BulkReview(r.Context(), principal.Identity.InstitutionID, offeringID,
		principal.Identity.UserID, request.StudentIDs, request.Status)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	principal, ok := principal(w, r)
	if !ok {
		return
	}
	offeringID, ok := pathUUID(w, chi.URLParam(r, "id"), "Offering")
	if !ok {
		return
	}
	if principal.Identity.StudentID == "" {
		httpx.WriteError(w, http.StatusForbidden, "student_required", "Only a claimed student can register", nil)
		return
	}
	result, err := h.service.Register(r.Context(), principal.Identity.InstitutionID, offeringID, principal.Identity.StudentID)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := principal(w, r)
	if !ok {
		return
	}
	offeringID, ok := pathUUID(w, chi.URLParam(r, "id"), "Offering")
	if !ok {
		return
	}
	limit, valid := queryInt(r, "limit", 50)
	if !valid {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer", nil)
		return
	}
	offset, valid := queryInt(r, "offset", 0)
	if !valid {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "offset must be an integer", nil)
		return
	}
	result, err := h.service.List(r.Context(), principal.Identity.InstitutionID, offeringID,
		principal.Identity.UserID, r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) review(w http.ResponseWriter, r *http.Request) {
	principal, ok := principal(w, r)
	if !ok {
		return
	}
	offeringID, ok := pathUUID(w, chi.URLParam(r, "id"), "Offering")
	if !ok {
		return
	}
	studentID, ok := pathUUID(w, chi.URLParam(r, "student_id"), "Student")
	if !ok {
		return
	}
	var request struct {
		Status string `json:"status"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	result, err := h.service.Review(r.Context(), principal.Identity.InstitutionID,
		offeringID, studentID, principal.Identity.UserID, request.Status)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func principal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	value, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return auth.Principal{}, false
	}
	return value, true
}

func pathUUID(w http.ResponseWriter, value, resource string) (string, bool) {
	if _, err := db.ParseUUID(value); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", resource+" ID is invalid", nil)
		return "", false
	}
	return value, true
}

func queryInt(r *http.Request, key string, fallback int) (int, bool) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback, true
	}
	number, err := strconv.Atoi(value)
	return number, err == nil
}

func writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, registration.ErrInvalidReview):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Registration status is invalid", nil)
	case errors.Is(err, registration.ErrRegistrationClosed):
		httpx.WriteError(w, http.StatusConflict, "registration_closed", "Registration for this offering is closed", nil)
	case errors.Is(err, registration.ErrOfferingNotFound):
		httpx.WriteError(w, http.StatusNotFound, "offering_not_found", "Offering or student was not found", nil)
	case errors.Is(err, registration.ErrRegistrationNotFound):
		httpx.WriteError(w, http.StatusNotFound, "registration_not_found", "Registration was not found or is no longer pending", nil)
	case errors.Is(err, registration.ErrAccessDenied):
		httpx.WriteError(w, http.StatusForbidden, "registration_access_denied", "You cannot manage registrations for this offering", nil)
	default:
		slog.ErrorContext(r.Context(), "registration request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to complete registration request", nil)
	}
}
