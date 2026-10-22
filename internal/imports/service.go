package imports

import (
	"context"
	"errors"
	"fmt"
	"io"
)

var (
	ErrImportPreviewHasErrors = errors.New("student import contains invalid rows")
	ErrImportChanged          = errors.New("student import validation changed before commit")
	ErrDuplicateMatric        = errors.New("matric number was added after validation")
	ErrDuplicateStaff         = errors.New("staff number was added after validation")
)

type StudentStore interface {
	ValidateStudents(context.Context, string, string, []StudentRow) ([]RowError, error)
	CommitStudents(context.Context, string, string, []StudentRow) error
}

type Service struct {
	store   StudentStore
	maxSize int64
}

func NewService(store StudentStore, maxSize int64) (*Service, error) {
	if store == nil {
		return nil, errors.New("student store is required")
	}
	if maxSize <= 0 || maxSize > MaxStudentCSVBytes {
		maxSize = MaxStudentCSVBytes
	}
	return &Service{store: store, maxSize: maxSize}, nil
}

func (s *Service) Preview(source io.Reader) (Preview, error) {
	return PreviewStudents(source, s.maxSize)
}

func (s *Service) PreviewForUser(ctx context.Context, institutionID, userID string, source io.Reader) (Preview, error) {
	if institutionID == "" || userID == "" {
		return Preview{}, errors.New("institution and user IDs are required")
	}
	preview, err := s.Preview(source)
	if err != nil {
		return Preview{}, err
	}
	if len(preview.Rows) == 0 {
		return preview, nil
	}
	storeErrors, err := s.store.ValidateStudents(ctx, institutionID, userID, preview.Rows)
	if err != nil {
		return Preview{}, fmt.Errorf("validate student import: %w", err)
	}
	preview.Errors = append(preview.Errors, storeErrors...)
	return preview, nil
}

func (s *Service) Commit(ctx context.Context, institutionID, userID string, source io.Reader) (Preview, error) {
	preview, err := s.PreviewForUser(ctx, institutionID, userID, source)
	if err != nil {
		return Preview{}, err
	}
	if len(preview.Errors) > 0 {
		return preview, ErrImportPreviewHasErrors
	}
	if err := s.store.CommitStudents(ctx, institutionID, userID, preview.Rows); err != nil {
		return preview, fmt.Errorf("commit student import: %w", err)
	}
	return preview, nil
}

func (s *Service) PreviewStaff(source io.Reader) (StaffPreview, error) {
	return PreviewStaff(source, s.maxSize)
}

func (s *Service) PreviewStaffForUser(ctx context.Context, institutionID, userID string, source io.Reader) (StaffPreview, error) {
	if institutionID == "" || userID == "" {
		return StaffPreview{}, errors.New("institution and user IDs are required")
	}
	store, ok := s.store.(StaffStore)
	if !ok {
		return StaffPreview{}, errors.New("staff import store is not configured")
	}
	preview, err := s.PreviewStaff(source)
	if err != nil {
		return StaffPreview{}, err
	}
	if len(preview.Rows) == 0 {
		return preview, nil
	}
	storeErrors, err := store.ValidateStaff(ctx, institutionID, userID, preview.Rows)
	if err != nil {
		return StaffPreview{}, fmt.Errorf("validate staff import: %w", err)
	}
	preview.Errors = append(preview.Errors, storeErrors...)
	return preview, nil
}

func (s *Service) CommitStaff(ctx context.Context, institutionID, userID string, source io.Reader) (StaffPreview, error) {
	store, ok := s.store.(StaffStore)
	if !ok {
		return StaffPreview{}, errors.New("staff import store is not configured")
	}
	preview, err := s.PreviewStaffForUser(ctx, institutionID, userID, source)
	if err != nil {
		return StaffPreview{}, err
	}
	if len(preview.Errors) > 0 {
		return preview, ErrImportPreviewHasErrors
	}
	if err := store.CommitStaff(ctx, institutionID, userID, preview.Rows); err != nil {
		return preview, fmt.Errorf("commit staff import: %w", err)
	}
	return preview, nil
}
