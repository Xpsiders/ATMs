package auth

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("email or password is invalid")
	ErrEmailNotVerified   = errors.New("student email has not been verified")
	ErrCurrentPassword    = errors.New("current password is invalid")
)

type LoginRecord struct {
	Identity      Identity
	PasswordHash  string
	EmailVerified bool
}

type LoginRepository interface {
	FindLogin(context.Context, string, string) (LoginRecord, bool, error)
	FindUserProfile(context.Context, string, string) (UserProfile, bool, error)
}

type UserProfile struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Email              string     `json:"email"`
	Status             string     `json:"status"`
	MustChangePassword bool       `json:"must_change_password"`
	Roles              []UserRole `json:"roles"`
}

type UserRole struct {
	Role         string  `json:"role"`
	ScopeType    string  `json:"scope_type"`
	ScopeID      *string `json:"scope_id"`
	DepartmentID string  `json:"department_id,omitempty"`
	Level        *int    `json:"level,omitempty"`
}

type PasswordRepository interface {
	FindPasswordHash(context.Context, string, string) (string, bool, error)
	UpdatePasswordHash(context.Context, string, string, string) (bool, error)
}

type LoginService struct {
	repository LoginRepository
	tokens     *TokenManager
	dummyHash  []byte
}

type TokenPair struct {
	AccessToken  string      `json:"access_token"`
	ExpiresIn    int         `json:"expires_in"`
	ForceChange  bool        `json:"force_password_change"`
	RefreshToken string      `json:"-"`
	Profile      UserProfile `json:"-"`
}

type LoginResponse struct {
	AccessToken        string     `json:"access_token"`
	ExpiresIn          int        `json:"expires_in"`
	ForceChange        bool       `json:"force_password_change"`
	MustChangePassword bool       `json:"must_change_password"`
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Email              string     `json:"email"`
	Status             string     `json:"status"`
	Roles              []UserRole `json:"roles"`
}

func NewLoginService(repository LoginRepository, tokens *TokenManager) (*LoginService, error) {
	if repository == nil || tokens == nil {
		return nil, errors.New("login repository and token manager are required")
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("not-a-real-user-password"), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("prepare credential verification: %w", err)
	}
	return &LoginService{repository: repository, tokens: tokens, dummyHash: dummyHash}, nil
}

func (s *LoginService) Login(ctx context.Context, institutionID, email, password string) (TokenPair, error) {
	if institutionID == "" || email == "" || password == "" {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return TokenPair{}, ErrInvalidCredentials
	}
	record, found, err := s.repository.FindLogin(ctx, institutionID, email)
	if err != nil {
		return TokenPair{}, fmt.Errorf("load login credentials: %w", err)
	}
	hash := s.dummyHash
	if found {
		hash = []byte(record.PasswordHash)
	}
	passwordMatches := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if !found || !passwordMatches {
		return TokenPair{}, ErrInvalidCredentials
	}
	if record.Identity.StudentID != "" && !record.EmailVerified {
		return TokenPair{}, ErrEmailNotVerified
	}
	profile, found, err := s.findUserProfile(ctx, record.Identity.InstitutionID, record.Identity.UserID)
	if err != nil {
		return TokenPair{}, fmt.Errorf("load login user profile: %w", err)
	}
	if !found || profile.Status != "active" {
		return TokenPair{}, ErrInvalidCredentials
	}
	access, err := s.tokens.IssueAccess(record.Identity)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := s.tokens.IssueRefresh(ctx, record.Identity)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken: access, ExpiresIn: int(AccessTokenLifetime.Seconds()),
		ForceChange: record.Identity.ForcePassword, RefreshToken: refresh, Profile: profile,
	}, nil
}

func (s *LoginService) UserProfile(ctx context.Context, institutionID, userID string) (UserProfile, bool, error) {
	return s.findUserProfile(ctx, institutionID, userID)
}

func (s *LoginService) findUserProfile(ctx context.Context, institutionID, userID string) (UserProfile, bool, error) {
	profile, found, err := s.repository.FindUserProfile(ctx, institutionID, userID)
	if err != nil {
		return UserProfile{}, false, fmt.Errorf("find user profile: %w", err)
	}
	return profile, found, nil
}

func (s *LoginService) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	rotated, identity, err := s.tokens.RotateRefresh(ctx, refreshToken)
	if err != nil {
		return TokenPair{}, err
	}
	access, err := s.tokens.IssueAccess(identity)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken: access, ExpiresIn: int(AccessTokenLifetime.Seconds()),
		ForceChange: identity.ForcePassword, RefreshToken: rotated,
	}, nil
}

func (s *LoginService) Logout(ctx context.Context, refreshToken string) error {
	return s.tokens.RevokeRefresh(ctx, refreshToken)
}

func (s *LoginService) ChangePassword(ctx context.Context, identity Identity, current, next string) (TokenPair, error) {
	repository, ok := s.repository.(PasswordRepository)
	if !ok {
		return TokenPair{}, errors.New("password changes are not supported by this repository")
	}
	hash, found, err := repository.FindPasswordHash(ctx, identity.InstitutionID, identity.UserID)
	if err != nil {
		return TokenPair{}, fmt.Errorf("load current password: %w", err)
	}
	if !found || !VerifyPassword(hash, current) {
		return TokenPair{}, ErrCurrentPassword
	}
	newHash, err := HashPassword(next)
	if err != nil {
		return TokenPair{}, err
	}
	updated, err := repository.UpdatePasswordHash(ctx, identity.InstitutionID, identity.UserID, newHash)
	if err != nil {
		return TokenPair{}, fmt.Errorf("update password: %w", err)
	}
	if !updated {
		return TokenPair{}, errors.New("user account was not updated")
	}
	identity.ForcePassword = false
	access, err := s.tokens.IssueAccess(identity)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: access, ExpiresIn: int(AccessTokenLifetime.Seconds())}, nil
}
