package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"attendance-api/internal/notify"
)

const ClaimCodeLifetime = 10 * time.Minute

var (
	ErrInvalidVerification   = errors.New("verification code is invalid, expired, or already used")
	ErrStudentAlreadyClaimed = errors.New("student record has already been claimed")
	ErrClaimEmailMismatch    = errors.New("student email does not match the institution record")
)

type StudentRecord struct {
	ID            string
	InstitutionID string
	DepartmentID  string
	MatricNo      string
	Email         string
	DisplayName   string
	Level         int
	Claimed       bool
}

type ClaimedStudent struct {
	UserID        string
	StudentID     string
	InstitutionID string
	DepartmentID  string
	Level         int
}

type ClaimRepository interface {
	FindStudentByMatric(context.Context, string, string) (StudentRecord, bool, error)
	CreateSignupRequest(context.Context, string, string, string) error
	CreateVerification(context.Context, string, string, []byte, time.Time) error
	ClaimStudent(context.Context, string, string, []byte, time.Time, string, string) (ClaimedStudent, error)
}

type ClaimService struct {
	repository ClaimRepository
	mailer     notify.Mailer
	clock      Clock
	random     io.Reader
	randomMu   sync.Mutex
	codeSecret []byte
}

type ClaimStartResult struct {
	ApprovalRequested bool
}

func NewClaimService(repository ClaimRepository, mailer notify.Mailer, clock Clock, codeSecret []byte) (*ClaimService, error) {
	if repository == nil || mailer == nil || clock == nil {
		return nil, errors.New("claim repository, mailer, and clock are required")
	}
	if len(codeSecret) < 32 {
		return nil, errors.New("claim code secret must contain at least 32 bytes")
	}
	return &ClaimService{
		repository: repository,
		mailer:     mailer,
		clock:      clock,
		random:     rand.Reader,
		codeSecret: append([]byte(nil), codeSecret...),
	}, nil
}

func (s *ClaimService) SetRandomSource(source io.Reader) error {
	if source == nil {
		return errors.New("random source is required")
	}
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	s.random = source
	return nil
}

func (s *ClaimService) Start(ctx context.Context, institutionID, matricNo, email string) (ClaimStartResult, error) {
	institutionID = strings.TrimSpace(institutionID)
	matricNo = strings.TrimSpace(matricNo)
	email = strings.TrimSpace(email)
	if institutionID == "" || matricNo == "" || email == "" {
		return ClaimStartResult{}, errors.New("institution, matric number, and email are required")
	}
	record, found, err := s.repository.FindStudentByMatric(ctx, institutionID, matricNo)
	if err != nil {
		return ClaimStartResult{}, fmt.Errorf("find student record: %w", err)
	}
	if !found {
		if err := s.repository.CreateSignupRequest(ctx, institutionID, matricNo, email); err != nil {
			return ClaimStartResult{}, fmt.Errorf("create signup request: %w", err)
		}
		return ClaimStartResult{ApprovalRequested: true}, nil
	}
	if !strings.EqualFold(strings.TrimSpace(record.Email), email) {
		return ClaimStartResult{}, ErrClaimEmailMismatch
	}
	if record.Claimed {
		return ClaimStartResult{}, ErrStudentAlreadyClaimed
	}

	code, err := s.newCode()
	if err != nil {
		return ClaimStartResult{}, err
	}
	if err := s.repository.CreateVerification(ctx, record.InstitutionID, record.ID, s.hashCode(record.ID, code),
		s.clock.Now().UTC().Add(ClaimCodeLifetime)); err != nil {
		return ClaimStartResult{}, fmt.Errorf("store email verification: %w", err)
	}
	message := notify.Message{
		To:      record.Email,
		Subject: "Verify your student account",
		Text:    "Your Campus Attendance verification code is " + code + ". It expires in 10 minutes.",
	}
	if err := s.mailer.Send(ctx, message); err != nil {
		return ClaimStartResult{}, fmt.Errorf("send verification email: %w", err)
	}
	return ClaimStartResult{}, nil
}

func (s *ClaimService) Verify(ctx context.Context, institutionID, matricNo, code, password string) (ClaimedStudent, error) {
	if strings.TrimSpace(institutionID) == "" || strings.TrimSpace(matricNo) == "" || len(code) != 6 {
		return ClaimedStudent{}, ErrInvalidVerification
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return ClaimedStudent{}, err
	}
	record, found, err := s.repository.FindStudentByMatric(ctx, institutionID, matricNo)
	if err != nil {
		return ClaimedStudent{}, fmt.Errorf("find student record: %w", err)
	}
	if !found {
		return ClaimedStudent{}, ErrInvalidVerification
	}
	claimed, err := s.repository.ClaimStudent(ctx, institutionID, record.ID, s.hashCode(record.ID, code),
		s.clock.Now().UTC(), passwordHash, record.DisplayName)
	if err != nil {
		return ClaimedStudent{}, err
	}
	return claimed, nil
}

func (s *ClaimService) newCode() (string, error) {
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	const codeSpace = uint32(1_000_000)
	const limit = ^uint32(0) - (^uint32(0) % codeSpace)
	for {
		var bytes [4]byte
		if _, err := io.ReadFull(s.random, bytes[:]); err != nil {
			return "", fmt.Errorf("generate verification code: %w", err)
		}
		value := binary.BigEndian.Uint32(bytes[:])
		if value < limit {
			return fmt.Sprintf("%06d", value%codeSpace), nil
		}
	}
}

func (s *ClaimService) hashCode(recordID, code string) []byte {
	mac := hmac.New(sha256.New, s.codeSecret)
	_, _ = mac.Write([]byte(recordID + ":" + code))
	return mac.Sum(nil)
}
