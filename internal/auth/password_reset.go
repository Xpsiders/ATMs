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
)

const (
	PasswordResetLifetime = 15 * time.Minute
	PasswordResetMaxTries = 5
)

var ErrInvalidPasswordReset = errors.New("password reset code is invalid, expired, or exhausted")

type PasswordResetRepository interface {
	CreatePasswordReset(context.Context, string, string, []byte, string, time.Time) error
	CompletePasswordReset(context.Context, string, string, []byte, time.Time, string) error
}

type PasswordResetService struct {
	repository PasswordResetRepository
	clock      Clock
	random     io.Reader
	randomMu   sync.Mutex
	secret     []byte
}

func NewPasswordResetService(repository PasswordResetRepository, clock Clock, secret []byte) (*PasswordResetService, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("password reset repository and clock are required")
	}
	if len(secret) < 32 {
		return nil, errors.New("password reset secret must contain at least 32 bytes")
	}
	return &PasswordResetService{
		repository: repository, clock: clock, random: rand.Reader, secret: append([]byte(nil), secret...),
	}, nil
}

func (s *PasswordResetService) SetRandomSource(source io.Reader) error {
	if source == nil {
		return errors.New("random source is required")
	}
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	s.random = source
	return nil
}

func (s *PasswordResetService) Forgot(ctx context.Context, institutionID, email string) error {
	institutionID = strings.TrimSpace(institutionID)
	email = strings.ToLower(strings.TrimSpace(email))
	if institutionID == "" || email == "" {
		return errors.New("institution and email are required")
	}
	code, err := s.newCode()
	if err != nil {
		return err
	}
	if err := s.repository.CreatePasswordReset(ctx, institutionID, email,
		s.hash(institutionID, email, code), code, s.clock.Now().UTC().Add(PasswordResetLifetime)); err != nil {
		return fmt.Errorf("create password reset: %w", err)
	}
	return nil
}

func (s *PasswordResetService) Reset(ctx context.Context, institutionID, email, code, password string) error {
	institutionID = strings.TrimSpace(institutionID)
	email = strings.ToLower(strings.TrimSpace(email))
	if institutionID == "" || email == "" || len(code) != 6 {
		return ErrInvalidPasswordReset
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.repository.CompletePasswordReset(ctx, institutionID, email,
		s.hash(institutionID, email, code), s.clock.Now().UTC(), passwordHash); err != nil {
		return err
	}
	return nil
}

func (s *PasswordResetService) hash(institutionID, email, code string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(institutionID + ":" + strings.ToLower(email) + ":" + code))
	return mac.Sum(nil)
}

func (s *PasswordResetService) newCode() (string, error) {
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	const codeSpace = uint32(1_000_000)
	const limit = ^uint32(0) - (^uint32(0) % codeSpace)
	for {
		var raw [4]byte
		if _, err := io.ReadFull(s.random, raw[:]); err != nil {
			return "", fmt.Errorf("generate password reset code: %w", err)
		}
		value := binary.BigEndian.Uint32(raw[:])
		if value < limit {
			return fmt.Sprintf("%06d", value%codeSpace), nil
		}
	}
}

func passwordResetCodeMatches(expected, actual []byte) bool {
	return hmac.Equal(expected, actual)
}

func canAttemptPasswordReset(attempts int) bool {
	return attempts >= 0 && attempts < PasswordResetMaxTries
}
