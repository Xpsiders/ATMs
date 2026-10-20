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
	router.Get("/institutions", h.list(catalogue.Institutions))
	router.Get("/institutions/{id}", h.get(catalogue.Institutions))
	router.Patch("/institutions/{id}", h.update(catalogue.Institutions))
	router.Get("/offerings/search", h.searchOfferings)
	router.Get("/offerings/{id}/lecturers", h.offeringLecturers)
	router.Put("/offerings/{id}/lecturers", h.setOfferingLecturers)
	router.Get("/offerings/{id}/auto-targets", h.offeringAutoTargets)
	router.Put("/offerings/{id}/auto-targets", h.setOfferingAutoTargets)
}

func (h *Handler) registerResource(router chi.Router, path string, resource catalogue.Resource) {
	router.Get("/"+path, h.list(resource))
	router.Post("/"+path, h.create(resource))
	router.Get("/"+path+"/{id}", h.get(resource))
	router.Patch("/"+path+"/{id}", h.update(resource))
	router.Delete("/"+path+"/{id}", h.delete(resource))
}

func (h *Handler) list(resource catalogue.Resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := requirePrincipal(w, r)
		if !ok {
			return
		}
		filters := map[string]string{}
		for _, key := range []string{"id", "faculty_id", "department_id", "programme_id", "academic_session_id", "semester_id", "course_id", "level", "registration_mode", "current", "q"} {
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
	case errors.Is(err, catalogue.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "Resource was not found", nil)
	default:
		slog.ErrorContext(r.Context(), "catalogue request failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", message, nil)
	}
}
