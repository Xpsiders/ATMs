package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"attendance-api/internal/httpx"

	"github.com/go-chi/chi/v5"
)

// DirectoryHandler serves the scoped user-directory search endpoint.
type DirectoryHandler struct {
	service *DirectoryService
}

// NewDirectoryHandler validates dependencies and builds the handler.
func NewDirectoryHandler(service *DirectoryService) (*DirectoryHandler, error) {
	if service == nil {
		return nil, errors.New("directory service is required")
	}
	return &DirectoryHandler{service: service}, nil
}

// RegisterRoutes mounts the directory routes on an authenticated router.
func (h *DirectoryHandler) RegisterRoutes(router chi.Router) {
	router.Get("/users", h.list)
}

func (h *DirectoryHandler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
		return
	}
	limit, offset, ok := directoryPagination(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	items, hasMore, err := h.service.Search(r.Context(), principal.Identity, DirectoryFilter{
		Query:        query.Get("q"),
		Role:         query.Get("role"),
		DepartmentID: query.Get("department_id"),
		Limit:        limit,
		Offset:       offset,
	})
	switch {
	case errors.Is(err, ErrInvalidDirectoryQuery):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "User directory query parameters are invalid", nil)
		return
	case errors.Is(err, ErrDirectoryScopeDenied):
		httpx.WriteError(w, http.StatusForbidden, "directory_scope_denied", "You cannot read the user directory", nil)
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "list directory users failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to list users", nil)
		return
	}
	if items == nil {
		items = []DirectoryUser{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "limit": limit, "offset": offset, "has_more": hasMore,
	})
}

func directoryPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
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
