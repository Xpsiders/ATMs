package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"sync"
	"time"

	"attendance-api/internal/notify"
)

const InvitationLifetime = 7 * 24 * time.Hour

var (
	ErrInvitationScopeDenied    = errors.New("invitation role or scope is outside the caller's authority")
	ErrInvalidInvitation        = errors.New("invitation token is invalid, expired, or already used")
	ErrInvitationAccountExists  = errors.New("an account with this email already exists")
	ErrInvalidInvitationRequest = errors.New("invitation role, scope, or email is invalid")
)

type InvitationRequest struct {
	Email        string
	Role         string
	ScopeType    string
	FacultyID    string
	DepartmentID string
}

type InvitationRecord struct {
	ID        string
	ExpiresAt time.Time
}

type InvitationRepository interface {
	CreateInvitation(context.Context, Identity, InvitationRequest, []byte, time.Time) (InvitationRecord, error)
	AcceptInvitation(context.Context, string, []byte, time.Time, string, string) (Identity, error)
}

type InvitationService struct {
	repository InvitationRepository
	mailer     notify.Mailer
	clock      Clock
	random     io.Reader
	randomMu   sync.Mutex
}

func NewInvitationService(repository InvitationRepository, mailer notify.Mailer, clock Clock) (*InvitationService, error) {
	if repository == nil || mailer == nil || clock == nil {
		return nil, errors.New("invitation repository, mailer, and clock are required")
	}
	return &InvitationService{repository: repository, mailer: mailer, clock: clock, random: rand.Reader}, nil
}

func (s *InvitationService) SetRandomSource(source io.Reader) error {
	if source == nil {
		return errors.New("random source is required")
	}
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	s.random = source
	return nil
}

func (s *InvitationService) Create(ctx context.Context, issuer Identity, request InvitationRequest) (InvitationRecord, error) {
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	if err := validateInvitationRequest(request); err != nil {
		return InvitationRecord{}, err
	}
	if issuer.InstitutionID == "" || issuer.UserID == "" {
		return InvitationRecord{}, ErrInvitationScopeDenied
	}
	token, tokenHash, err := s.newToken()
	if err != nil {
		return InvitationRecord{}, err
	}
	expiresAt := s.clock.Now().UTC().Add(InvitationLifetime)
	invitation, err := s.repository.CreateInvitation(ctx, issuer, request, tokenHash, expiresAt)
	if err != nil {
		return InvitationRecord{}, err
	}
	if err := s.mailer.Send(ctx, notify.Message{
		To: request.Email, Subject: "Campus Attendance staff invitation",
		Text: fmt.Sprintf("You have been invited to Campus Attendance. Institution ID: %s. Use this invitation token to create your account: %s. It expires on %s.",
			issuer.InstitutionID, token, expiresAt.Format(time.RFC3339)),
	}); err != nil {
		return InvitationRecord{}, fmt.Errorf("send staff invitation: %w", err)
	}
	return invitation, nil
}

func (s *InvitationService) Accept(ctx context.Context, institutionID, token, displayName, password string) (Identity, error) {
	token = strings.TrimSpace(token)
	displayName = strings.TrimSpace(displayName)
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || displayName == "" || strings.TrimSpace(institutionID) == "" {
		return Identity{}, ErrInvalidInvitation
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return Identity{}, err
	}
	hash := sha256.Sum256([]byte(token))
	identity, err := s.repository.AcceptInvitation(ctx, institutionID, hash[:], s.clock.Now().UTC(), passwordHash, displayName)
	if err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (s *InvitationService) newToken() (string, []byte, error) {
	var raw [32]byte
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	if _, err := io.ReadFull(s.random, raw[:]); err != nil {
		return "", nil, fmt.Errorf("generate invitation token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func validateInvitationRequest(request InvitationRequest) error {
	address, err := mail.ParseAddress(request.Email)
	if err != nil || !strings.EqualFold(address.Address, request.Email) {
		return ErrInvalidInvitationRequest
	}
	switch request.Role {
	case "institution_admin":
		if request.ScopeType != "institution" || request.FacultyID != "" || request.DepartmentID != "" {
			return ErrInvalidInvitationRequest
		}
	case "faculty_admin":
		if request.ScopeType != "faculty" || request.FacultyID == "" || request.DepartmentID != "" {
			return ErrInvalidInvitationRequest
		}
	case "department_admin", "lecturer":
		if request.ScopeType != "department" || request.FacultyID != "" || request.DepartmentID == "" {
			return ErrInvalidInvitationRequest
		}
	default:
		return ErrInvalidInvitationRequest
	}
	return nil
}
