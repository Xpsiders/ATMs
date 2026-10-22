package auth

import (
	"context"
	"errors"
	"testing"
)

type signupRequestFake struct {
	items    []SignupRequest
	err      error
	reviewed string
}

func (f *signupRequestFake) ListSignupRequests(_ context.Context, _ Identity, limit, offset int) ([]SignupRequest, error) {
	if f.err != nil {
		return nil, f.err
	}
	end := offset + limit
	if offset >= len(f.items) {
		return []SignupRequest{}, nil
	}
	if end > len(f.items) {
		end = len(f.items)
	}
	return f.items[offset:end], nil
}

func (f *signupRequestFake) ReviewSignupRequest(_ context.Context, _ Identity, _, status, _ string, _ int, _, _ string) error {
	f.reviewed = status
	return f.err
}

func TestSignupRequestListPaginatesWithHasMore(t *testing.T) {
	repository := &signupRequestFake{items: []SignupRequest{
		{ID: "1"}, {ID: "2"}, {ID: "3"},
	}}
	service, err := NewSignupRequestService(repository)
	if err != nil {
		t.Fatal(err)
	}
	items, hasMore, err := service.List(context.Background(), Identity{InstitutionID: "i1", UserID: "u1"}, 2, 0)
	if err != nil || !hasMore || len(items) != 2 {
		t.Fatalf("List() = %d items, hasMore=%v, err=%v", len(items), hasMore, err)
	}
}

func TestSignupApprovalRequiresCompleteStudentDetails(t *testing.T) {
	repository := &signupRequestFake{}
	service, err := NewSignupRequestService(repository)
	if err != nil {
		t.Fatal(err)
	}
	err = service.Review(context.Background(), Identity{InstitutionID: "i1", UserID: "u1"},
		"request-1", "approved", "department-1", 200, "", "")
	if !errors.Is(err, ErrInvalidSignupReview) || repository.reviewed != "" {
		t.Fatalf("incomplete approval error=%v, reviewed=%q", err, repository.reviewed)
	}
	err = service.Review(context.Background(), Identity{InstitutionID: "i1", UserID: "u1"},
		"request-1", "rejected", "", 0, "", "")
	if err != nil || repository.reviewed != "rejected" {
		t.Fatalf("rejection error=%v, reviewed=%q", err, repository.reviewed)
	}
}
