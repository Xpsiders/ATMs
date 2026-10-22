package httpapi

import (
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"attendance-api/internal/auth"
	"attendance-api/internal/httpx"
	"attendance-api/internal/imports"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *imports.Service
}

func NewHandler(service *imports.Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("student import service is required")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Post("/imports/students/preview", h.preview)
	router.Post("/imports/students", h.commit)
	router.Post("/imports/staff/preview", h.previewStaff)
	router.Post("/imports/staff", h.commitStaff)
}

func (h *Handler) previewStaff(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok || !requireCSV(w, r) {
		return
	}
	preview, err := h.service.PreviewStaffForUser(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, r.Body)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, preview)
}

func (h *Handler) commitStaff(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok || !requireCSV(w, r) {
		return
	}
	preview, err := h.service.CommitStaff(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, r.Body)
	if errors.Is(err, imports.ErrImportPreviewHasErrors) {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, preview)
		return
	}
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, preview)
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok || !requireCSV(w, r) {
		return
	}
	preview, err := h.service.PreviewForUser(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, r.Body)
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, preview)
}

func (h *Handler) commit(w http.ResponseWriter, r *http.Request) {
	principal, ok := requirePrincipal(w, r)
	if !ok || !requireCSV(w, r) {
		return
	}
	preview, err := h.service.Commit(r.Context(), principal.Identity.InstitutionID,
		principal.Identity.UserID, r.Body)
	if errors.Is(err, imports.ErrImportPreviewHasErrors) {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, preview)
		return
	}
	if err != nil {
		writeFailure(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, preview)
}

func requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return auth.Principal{}, false
	}
	return principal, true
}

func requireCSV(w http.ResponseWriter, r *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "text/csv") {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "Content-Type must be text/csv", nil)
		return false
	}
	return true
}

func writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, imports.ErrCSVTooLarge):
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "CSV exceeds the 5 MiB limit", nil)
	case errors.Is(err, imports.ErrInvalidCSV):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_csv", "CSV header or structure is invalid", nil)
	case errors.Is(err, imports.ErrImportChanged):
		httpx.WriteError(w, http.StatusConflict, "import_validation_changed", "Import data changed after validation; preview and retry the commit", nil)
	case errors.Is(err, imports.ErrDuplicateMatric):
		httpx.WriteError(w, http.StatusConflict, "matric_number_conflict", "A matric number was added after validation; preview and retry the import", nil)
	case errors.Is(err, imports.ErrDuplicateStaff):
		httpx.WriteError(w, http.StatusConflict, "staff_number_conflict", "A staff number was added after validation; preview and retry the import", nil)
	default:
		slog.ErrorContext(r.Context(), "CSV import failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to process student import", nil)
	}
}
