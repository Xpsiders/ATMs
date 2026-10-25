package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"attendance-api/internal/auth"
	"attendance-api/internal/catalogue"
	"attendance-api/internal/db"
	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *catalogue.Service
}

func NewHandler(service *catalogue.Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("catalogue service is required")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	for _, resource := range []catalogue.Resource{
		catalogue.Faculties, catalogue.Departments, catalogue.Programmes,
		catalogue.AcademicSessions, catalogue.Semesters, catalogue.Courses, catalogue.Offerings,
	} {
		h.registerResource(router, string(resource), resource)
	}
	router.Get("/me/offerings", h.myOfferings)
	router.Get("/venues", h.venues)
	router.Post("/venues", h.createVenue)
	router.Patch("/venues/{id}", h.updateVenue)
	router.Delete("/venues/{id}", h.deactivateVenue)
	router.Get("/institutions", h.list(catalogue.Institutions))
	router.Get("/institutions/{id}", h.get(catalogue.Institutions))
	router.Patch("/institutions/{id}", h.update(catalogue.Institutions))
	router.Get("/offerings/search", h.searchOfferings)
	router.Get("/offerings/{id}/lecturers", h.offeringLecturers)
	router.Put("/offerings/{id}/lecturers", h.setOfferingLecturers)
	router.Get("/offerings/{id}/auto-targets", h.offeringAutoTargets)
	router.Put("/offerings/{id}/auto-targets", h.setOfferingAutoTargets)
	router.Get("/offerings/{id}/timetable", h.listTimetable)
	router.Post("/offerings/{id}/timetable", h.createTimetable)
	router.Put("/offerings/{id}/timetable/{slot_id}", h.updateTimetable)
	router.Delete("/offerings/{id}/timetable/{slot_id}", h.deleteTimetable)
	router.Get("/teaching-requests", h.listTeachingRequests)
	router.Patch("/teaching-requests/{id}", h.reviewTeachingRequest)
}

func (h *Handler) myOfferings(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	filters := map[string]string{}
	limit, offset, ok := addPagination(w, r, filters)
	if !ok {
		return
	}
	filters["limit"] = strconv.Itoa(limit + 1)
	filters["offset"] = strconv.Itoa(offset)
	items, err := h.service.List(r.Context(), principal.Identity.InstitutionID, principal.Identity.UserID,
		principal.Identity.StudentID, catalogue.Offerings, filters)
	if err != nil {
		writeFailure(w, r, err, "Unable to list current user's offerings")
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "limit": limit, "offset": offset, "has_more": hasMore,
	})
}

func (h *Handler) venues(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	limit, offset, ok := addPagination(w, r, map[string]string{})
	if !ok {
		return
	}
	page, err := h.service.ListVenues(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, limit, offset)
	if err != nil {
		writeFailure(w, r, err, "Unable to list institution venues")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) createVenue(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var input catalogue.VenueInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Venue fields are invalid", nil)
		return
	}
	venue, err := h.service.CreateVenue(r.Context(), principal.Identity.InstitutionID, principal.Identity.UserID, input)
	if err != nil {
		writeFailure(w, r, err, "Unable to create venue")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, venue)
}

func (h *Handler) updateVenue(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	venueID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(venueID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Venue ID is invalid", nil)
		return
	}
	var update catalogue.VenueUpdate
	if err := httpx.DecodeJSON(w, r, &update); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(update); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Venue fields are invalid", nil)
		return
	}
	venue, err := h.service.UpdateVenue(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, venueID, update)
	if err != nil {
		writeFailure(w, r, err, "Unable to update venue")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, venue)
}

func (h *Handler) deactivateVenue(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	venueID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(venueID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Venue ID is invalid", nil)
		return
	}
	if err := h.service.DeactivateVenue(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, venueID); err != nil {
		writeFailure(w, r, err, "Unable to deactivate venue")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) registerResource(router chi.Router, path string, resource catalogue.Resource) {
	router.Get("/"+path, h.list(resource))
	if resource == catalogue.Offerings {
		router.Post("/"+path, h.createLecturerOffering)
	} else {
		router.Post("/"+path, h.create(resource))
	}
	router.Get("/"+path+"/{id}", h.get(resource))
	router.Patch("/"+path+"/{id}", h.update(resource))
	router.Delete("/"+path+"/{id}", h.delete(resource))
}

func (h *Handler) createLecturerOffering(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var input catalogue.OfferingInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if input.LateCountsAsAttended == nil {
		value := true
		input.LateCountsAsAttended = &value
	}
	if err := httpx.ValidateStruct(input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Offering fields are invalid", nil)
		return
	}
	result, err := h.service.CreateLecturerOffering(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, input)
	if err != nil {
		writeFailure(w, r, err, "Unable to create offering or teaching request")
		return
	}
	status := http.StatusCreated
	if result["status"] == "pending" {
		status = http.StatusAccepted
	}
	httpx.WriteJSON(w, status, result)
}

func (h *Handler) listTeachingRequests(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	limit, offset, ok := readPagination(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListTeachingRequests(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, limit, offset)
	if err != nil {
		writeFailure(w, r, err, "Unable to list pending teaching requests")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) reviewTeachingRequest(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	requestID := chi.URLParam(r, "id")
	if _, err := db.ParseUUID(requestID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Teaching request ID is invalid", nil)
		return
	}
	var request struct {
		Status string `json:"status" validate:"required,oneof=approved rejected"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "status must be approved or rejected", nil)
		return
	}
	result, err := h.service.ReviewTeachingRequest(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, requestID, request.Status)
	if err != nil {
		writeFailure(w, r, err, "Unable to review teaching request")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func readPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 50, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer", nil)
			return 0, 0, false
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "offset must be an integer", nil)
			return 0, 0, false
		}
	}
	if limit < 1 || limit > 200 || offset < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be 1..200 and offset non-negative", nil)
		return 0, 0, false
	}
	return limit, offset, true
}

func (h *Handler) list(resource catalogue.Resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := requirePrincipal(w, r)
		if !ok {
			return
		}
		filters := map[string]string{}
		for _, key := range []string{"id", "faculty_id", "department_id", "programme_id", "academic_session_id", "semester_id", "course_id", "level", "kind", "registration_mode", "current", "q"} {
			if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
				filters[key] = value
			}
		}
		limit, offset, ok := addPagination(w, r, filters)
		if !ok {
			return
		}
		filters["limit"] = strconv.Itoa(limit + 1)
		items, err := h.service.List(r.Context(), principal.Identity.InstitutionID, principal.Identity.UserID,
			principal.Identity.StudentID, resource, filters)
		if err != nil {
			writeFailure(w, r, err, "Unable to list "+string(resource))
			return
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset, "has_more": hasMore})
	}
}

func (h *Handler) searchOfferings(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	filters := map[string]string{"q": strings.TrimSpace(r.URL.Query().Get("q"))}
	if filters["q"] == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "q is required", nil)
		return
	}
	for _, key := range []string{"department_id", "level", "kind", "semester"} {
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			filters[key] = value
		}
	}
	if principal.Identity.StudentID != "" {
		if filters["department_id"] == "" && principal.Identity.DepartmentID != "" {
			filters["department_id"] = principal.Identity.DepartmentID
		}
		if filters["level"] == "" && principal.Identity.Level > 0 {
			filters["level"] = strconv.Itoa(principal.Identity.Level)
		}
	}
	limit, offset, ok := addPagination(w, r, filters)
	if !ok {
		return
	}
	filters["limit"] = strconv.Itoa(limit + 1)
	items, err := h.service.List(r.Context(), principal.Identity.InstitutionID, principal.Identity.UserID,
		principal.Identity.StudentID, catalogue.Offerings, filters)
	if err != nil {
		writeFailure(w, r, err, "Unable to search offerings")
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset, "has_more": hasMore})
}

func addPagination(w http.ResponseWriter, r *http.Request, filters map[string]string) (int, int, bool) {
	limit, offset := 50, 0
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer", nil)
			return 0, 0, false
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "offset must be an integer", nil)
			return 0, 0, false
		}
	}
	if limit < 1 || limit > 200 || offset < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be 1..200 and offset non-negative", nil)
		return 0, 0, false
	}
	filters["limit"] = strconv.Itoa(limit + 1)
	filters["offset"] = strconv.Itoa(offset)
	return limit, offset, true
}

func (h *Handler) get(resource catalogue.Resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := requirePrincipal(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := db.ParseUUID(id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Resource ID is invalid", nil)
			return
		}
		items, err := h.service.List(r.Context(), principal.Identity.InstitutionID, principal.Identity.UserID,
			principal.Identity.StudentID, resource, map[string]string{"id": id})
		if err != nil {
			writeFailure(w, r, err, "Unable to read "+string(resource))
			return
		}
		if len(items) == 0 {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "Resource was not found", nil)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, items[0])
	}
}

func (h *Handler) create(resource catalogue.Resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := requirePrincipal(w, r)
		if !ok {
			return
		}
		fields, ok := decodeFields(w, r)
		if !ok {
			return
		}
		result, err := h.service.Create(r.Context(), principal.Identity.InstitutionID,
			principal.Identity.UserID, resource, fields)
		if err != nil {
			writeFailure(w, r, err, "Unable to create "+strings.TrimSuffix(string(resource), "s"))
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, result)
	}
}

func (h *Handler) update(resource catalogue.Resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := requirePrincipal(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := db.ParseUUID(id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Resource ID is invalid", nil)
			return
		}
		fields, ok := decodeFields(w, r)
		if !ok {
			return
		}
		result, err := h.service.Update(r.Context(), principal.Identity.InstitutionID,
			principal.Identity.UserID, resource, id, fields)
		if err != nil {
			writeFailure(w, r, err, "Unable to update "+strings.TrimSuffix(string(resource), "s"))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, result)
	}
}

func (h *Handler) delete(resource catalogue.Resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := requirePrincipal(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := db.ParseUUID(id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Resource ID is invalid", nil)
			return
		}
		if err := h.service.Delete(r.Context(), principal.Identity.InstitutionID,
			principal.Identity.UserID, resource, id); err != nil {
			writeFailure(w, r, err, "Unable to delete "+strings.TrimSuffix(string(resource), "s"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) offeringLecturers(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireOffering(w, r)
	if !ok {
		return
	}
	items, err := h.service.OfferingLecturers(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, chi.URLParam(r, "id"))
	if err != nil {
		writeFailure(w, r, err, "Unable to list offering lecturers")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) setOfferingLecturers(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireOffering(w, r)
	if !ok {
		return
	}
	var request struct {
		LecturerIDs []string `json:"lecturer_ids"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if request.LecturerIDs == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "lecturer_ids must be an array", nil)
		return
	}
	for _, id := range request.LecturerIDs {
		if _, err := db.ParseUUID(id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "A lecturer ID is invalid", nil)
			return
		}
	}
	err := h.service.SetOfferingLecturers(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, chi.URLParam(r, "id"), request.LecturerIDs)
	if err != nil {
		writeFailure(w, r, err, "Unable to assign offering lecturers")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"lecturer_ids": request.LecturerIDs})
}

func (h *Handler) offeringAutoTargets(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireOffering(w, r)
	if !ok {
		return
	}
	items, err := h.service.OfferingAutoTargets(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, chi.URLParam(r, "id"))
	if err != nil {
		writeFailure(w, r, err, "Unable to list offering auto-registration targets")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) setOfferingAutoTargets(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireOffering(w, r)
	if !ok {
		return
	}
	var request struct {
		Targets []catalogue.AutoTarget `json:"targets"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if request.Targets == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "targets must be an array", nil)
		return
	}
	for _, target := range request.Targets {
		if err := httpx.ValidateStruct(target); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Each target needs a department ID and positive level", nil)
			return
		}
	}
	if err := h.service.SetOfferingAutoTargets(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, chi.URLParam(r, "id"), request.Targets); err != nil {
		writeFailure(w, r, err, "Unable to update offering auto-registration targets")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"targets": request.Targets})
}

func (h *Handler) listTimetable(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireOffering(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListTimetable(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, chi.URLParam(r, "id"))
	if err != nil {
		writeFailure(w, r, err, "Unable to list offering timetable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createTimetable(w http.ResponseWriter, r *http.Request) {
	h.saveTimetable(w, r, false)
}

func (h *Handler) updateTimetable(w http.ResponseWriter, r *http.Request) {
	h.saveTimetable(w, r, true)
}

func (h *Handler) saveTimetable(w http.ResponseWriter, r *http.Request, update bool) {
	principal, ok := requireOffering(w, r)
	if !ok {
		return
	}
	slotID := ""
	if update {
		slotID = chi.URLParam(r, "slot_id")
		if _, err := db.ParseUUID(slotID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Timetable slot ID is invalid", nil)
			return
		}
	}
	var input catalogue.TimetableInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return
	}
	if err := httpx.ValidateStruct(input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Timetable slot fields are invalid", nil)
		return
	}
	slot, err := h.service.SaveTimetable(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, chi.URLParam(r, "id"), slotID, input)
	if err != nil {
		writeFailure(w, r, err, "Unable to save timetable slot")
		return
	}
	status := http.StatusOK
	if !update {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, slot)
}

func (h *Handler) deleteTimetable(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireOffering(w, r)
	if !ok {
		return
	}
	slotID := chi.URLParam(r, "slot_id")
	if _, err := db.ParseUUID(slotID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Timetable slot ID is invalid", nil)
		return
	}
	if err := h.service.DeleteTimetable(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, chi.URLParam(r, "id"), slotID); err != nil {
		writeFailure(w, r, err, "Unable to delete timetable slot")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func requireOffering(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := requirePrincipal(w, r)
	if !ok {
		return auth.Principal{}, false
	}
	if _, err := db.ParseUUID(chi.URLParam(r, "id")); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Offering ID is invalid", nil)
		return auth.Principal{}, false
	}
	return principal, true
}

func requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return auth.Principal{}, false
	}
	return principal, true
}

func decodeFields(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	fields := map[string]any{}
	if err := httpx.DecodeJSON(w, r, &fields); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is invalid", nil)
		return nil, false
	}
	if len(fields) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "At least one field is required", nil)
		return nil, false
	}
	for key, value := range fields {
		if number, ok := value.(float64); ok {
			switch key {
			case "credits", "level":
				if number != float64(int64(number)) {
					httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Integer fields must be whole numbers", nil)
					return nil, false
				}
				fields[key] = int32(number)
			}
		}
	}
	return fields, true
}

func writeFailure(w http.ResponseWriter, r *http.Request, err error, message string) {
	switch {
	case errors.Is(err, catalogue.ErrInvalidRequest):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Resource fields are invalid", nil)
	case errors.Is(err, catalogue.ErrForbidden):
		httpx.WriteError(w, http.StatusForbidden, "access_denied", "You cannot manage this resource", nil)
	case errors.Is(err, catalogue.ErrResourceInUse):
		httpx.WriteError(w, http.StatusConflict, "resource_in_use", "This resource is still referenced by other records; remove them first", nil)
	case errors.Is(err, catalogue.ErrTimetableOverlap):
		httpx.WriteError(w, http.StatusConflict, "timetable_overlap", "Another slot overlaps at this venue", nil)
	case errors.Is(err, catalogue.ErrSlotNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Timetable slot was not found", nil)
	case errors.Is(err, catalogue.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Resource was not found", nil)
	default:
		slog.ErrorContext(r.Context(), "catalogue request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", message, nil)
	}
}
