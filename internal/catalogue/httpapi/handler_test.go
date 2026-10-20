package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"attendance-api/internal/auth"
	"attendance-api/internal/catalogue"

	"github.com/go-chi/chi/v5"
)

type endpointStore struct {
	offerings        []map[string]any
	listInstitution  string
	listUser         string
	listResource     catalogue.Resource
	listFilters      map[string]string
	venues           []map[string]any
	venueTotal       int64
	venueInstitution string
	venueUser        string
	venueErr         error
}

func (s *endpointStore) List(_ context.Context, institutionID, userID, _ string, resource catalogue.Resource, filters map[string]string) ([]map[string]any, error) {
	s.listInstitution, s.listUser, s.listResource, s.listFilters = institutionID, userID, resource, filters
	return s.offerings, nil
}
func (*endpointStore) Create(context.Context, string, string, catalogue.Resource, map[string]any) (map[string]any, error) {
	return nil, errors.New("not implemented")
}
func (*endpointStore) Update(context.Context, string, string, catalogue.Resource, string, map[string]any) (map[string]any, error) {
	return nil, errors.New("not implemented")
}
func (*endpointStore) Delete(context.Context, string, string, catalogue.Resource, string) error {
	return errors.New("not implemented")
}
func (*endpointStore) CanManage(context.Context, string, string, catalogue.Resource, string, map[string]any) (bool, error) {
	return false, errors.New("not implemented")
}
func (*endpointStore) CanReadOffering(context.Context, string, string, string) (bool, error) {
	return false, errors.New("not implemented")
}
func (*endpointStore) SetOfferingLecturers(context.Context, string, string, string, []string) error {
	return errors.New("not implemented")
}
func (*endpointStore) SetOfferingAutoTargets(context.Context, string, string, []catalogue.AutoTarget) error {
	return errors.New("not implemented")
}
func (*endpointStore) OfferingLecturers(context.Context, string, string) ([]map[string]any, error) {
	return nil, errors.New("not implemented")
}
func (*endpointStore) OfferingAutoTargets(context.Context, string, string) ([]catalogue.AutoTarget, error) {
	return nil, errors.New("not implemented")
}
func (s *endpointStore) ListVenues(_ context.Context, institutionID, userID string, _, _ int) ([]map[string]any, int64, error) {
	s.venueInstitution, s.venueUser = institutionID, userID
	return s.venues, s.venueTotal, s.venueErr
}

func testRouter(t *testing.T, store *endpointStore) *chi.Mux {
	t.Helper()
	service, err := catalogue.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	return router
}

func withIdentity(request *http.Request, identity auth.Identity) *http.Request {
	ctx := auth.WithPrincipal(request.Context(), auth.Principal{Identity: identity})
	return request.WithContext(ctx)
}

func TestMyOfferingsUsesAuthenticatedLecturerScopeAndPagination(t *testing.T) {
	t.Parallel()
	store := &endpointStore{
		offerings: []map[string]any{{"id": "assigned"}, {"id": "next"}},
	}
	router := testRouter(t, store)
	request := httptest.NewRequest(http.MethodGet, "/me/offerings?limit=1&offset=4", nil)
	request = withIdentity(request, auth.Identity{InstitutionID: "tenant", UserID: "lecturer"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.listInstitution != "tenant" || store.listUser != "lecturer" ||
		store.listResource != catalogue.Offerings || store.listFilters["limit"] != "2" ||
		store.listFilters["offset"] != "4" {
		t.Fatalf("list scope/filter = %q %q %q %v", store.listInstitution, store.listUser, store.listResource, store.listFilters)
	}
	var body struct {
		Items   []map[string]any `json:"items"`
		Limit   int              `json:"limit"`
		Offset  int              `json:"offset"`
		HasMore bool             `json:"has_more"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Limit != 1 || body.Offset != 4 || !body.HasMore {
		t.Fatalf("unexpected paginated offerings response: %+v", body)
	}
}

func TestVenueListingUsesAuthenticatedTenantAndReturnsTotal(t *testing.T) {
	t.Parallel()
	store := &endpointStore{
		venues:     []map[string]any{{"id": "venue"}},
		venueTotal: 6,
	}
	router := testRouter(t, store)
	request := httptest.NewRequest(http.MethodGet, "/venues?limit=3&offset=2", nil)
	request = withIdentity(request, auth.Identity{InstitutionID: "tenant", UserID: "lecturer"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.venueInstitution != "tenant" || store.venueUser != "lecturer" {
		t.Fatalf("venue query scope = %q %q", store.venueInstitution, store.venueUser)
	}
	var body struct {
		Items  []map[string]any `json:"items"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
		Total  int64            `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Limit != 3 || body.Offset != 2 || body.Total != 6 {
		t.Fatalf("unexpected venue response: %+v", body)
	}
}

func TestVenueListingDeniesCallerWithoutVenueRole(t *testing.T) {
	t.Parallel()
	store := &endpointStore{venueErr: catalogue.ErrForbidden}
	router := testRouter(t, store)
	request := httptest.NewRequest(http.MethodGet, "/venues", nil)
	request = withIdentity(request, auth.Identity{InstitutionID: "tenant", UserID: "student"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
