package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/httprate"
	"github.com/golang-jwt/jwt/v5"

	"attendance-api/internal/httpx"
)

const AccessTokenLifetime = 15 * time.Minute
const RefreshTokenLifetime = 30 * 24 * time.Hour

var ErrInvalidRefresh = errors.New("refresh token is invalid, expired, or already used")

type Clock interface {
	Now() time.Time
}

type RefreshStore interface {
	Create(context.Context, []byte, Identity, time.Time) error
	Rotate(context.Context, []byte, []byte, time.Time, time.Time) (Identity, error)
	Revoke(context.Context, []byte, time.Time) error
}

type TokenManager struct {
	secret   []byte
	clock    Clock
	random   io.Reader
	randomMu sync.Mutex
	store    RefreshStore
}

type AccessClaims struct {
	InstitutionID string `json:"institution_id"`
	UserID        string `json:"user_id"`
	StudentID     string `json:"student_id,omitempty"`
	DepartmentID  string `json:"department_id,omitempty"`
	Level         int    `json:"level,omitempty"`
	ForcePassword bool   `json:"force_password_change"`
	jwt.RegisteredClaims
}

type Identity struct {
	InstitutionID string
	UserID        string
	StudentID     string
	DepartmentID  string
	Level         int
	ForcePassword bool
}

func NewTokenManager(secret []byte, clock Clock, store RefreshStore) (*TokenManager, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must contain at least 32 bytes")
	}
	if clock == nil {
		return nil, errors.New("clock is required")
	}
	if store == nil {
		return nil, errors.New("refresh token store is required")
	}
	return &TokenManager{secret: append([]byte(nil), secret...), clock: clock, random: rand.Reader, store: store}, nil
}

func (m *TokenManager) SetRandomSource(source io.Reader) error {
	if source == nil {
		return errors.New("random source is required")
	}
	m.random = source
	return nil
}

func (m *TokenManager) IssueAccess(identity Identity) (string, error) {
	if identity.InstitutionID == "" || identity.UserID == "" {
		return "", errors.New("institution ID and user ID are required")
	}
	now := m.clock.Now().UTC()
	claims := AccessClaims{
		InstitutionID: identity.InstitutionID,
		UserID:        identity.UserID,
		StudentID:     identity.StudentID,
		DepartmentID:  identity.DepartmentID,
		Level:         identity.Level,
		ForcePassword: identity.ForcePassword,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenLifetime)),
			Subject:   identity.UserID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	encoded, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return encoded, nil
}

func (m *TokenManager) ValidateAccess(encoded string) (*AccessClaims, error) {
	claims := &AccessClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return m.clock.Now().UTC() }),
	)
	token, err := parser.ParseWithClaims(encoded, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected JWT signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("validate access token: %w", err)
	}
	if token == nil || !token.Valid || claims.UserID == "" ||
		claims.Subject != claims.UserID || claims.InstitutionID == "" {
		return nil, errors.New("access token claims are invalid")
	}
	return claims, nil
}

func (m *TokenManager) IssueRefresh(ctx context.Context, identity Identity) (string, error) {
	if identity.InstitutionID == "" || identity.UserID == "" {
		return "", errors.New("institution ID and user ID are required")
	}
	raw, hash, err := m.newRefreshToken()
	if err != nil {
		return "", err
	}
	if err := m.store.Create(ctx, hash, identity,
		m.clock.Now().UTC().Add(RefreshTokenLifetime)); err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}
	return raw, nil
}

func (m *TokenManager) RotateRefresh(ctx context.Context, raw string) (string, Identity, error) {
	hash := hashRefreshToken(raw)
	replacement, replacementHash, err := m.newRefreshToken()
	if err != nil {
		return "", Identity{}, err
	}
	now := m.clock.Now().UTC()
	identity, err := m.store.Rotate(ctx, hash, replacementHash, now, now.Add(RefreshTokenLifetime))
	if err != nil {
		return "", Identity{}, err
	}
	return replacement, identity, nil
}

func (m *TokenManager) RevokeRefresh(ctx context.Context, raw string) error {
	if raw == "" {
		return ErrInvalidRefresh
	}
	if err := m.store.Revoke(ctx, hashRefreshToken(raw), m.clock.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func (m *TokenManager) newRefreshToken() (string, []byte, error) {
	var randomBytes [32]byte
	m.randomMu.Lock()
	defer m.randomMu.Unlock()
	if _, err := io.ReadFull(m.random, randomBytes[:]); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(randomBytes[:])
	return raw, hashRefreshToken(raw), nil
}

func hashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

type Principal struct {
	Identity Identity
}

type principalKey struct{}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

func UserAuth(manager *TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := strings.Fields(r.Header.Get("Authorization"))
			if len(header) != 2 || !strings.EqualFold(header[0], "Bearer") {
				httpx.WriteError(w, http.StatusUnauthorized, "authentication_required", "A bearer token is required", nil)
				return
			}

			claims, err := manager.ValidateAccess(header[1])
			if err != nil {
				slog.WarnContext(r.Context(), "request authentication failed", "error", err)
				httpx.WriteError(w, http.StatusUnauthorized, "invalid_token", "Access token is invalid or expired", nil)
				return
			}
			if claims.ForcePassword && r.URL.Path != "/auth/password/change" && r.URL.Path != "/auth/logout" {
				httpx.WriteError(w, http.StatusForbidden, "password_change_required", "Change your password before continuing", nil)
				return
			}
			identity := Identity{
				InstitutionID: claims.InstitutionID,
				UserID:        claims.UserID,
				StudentID:     claims.StudentID,
				DepartmentID:  claims.DepartmentID,
				Level:         claims.Level,
				ForcePassword: claims.ForcePassword,
			}
			ctx := WithPrincipal(r.Context(), Principal{Identity: identity})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserRateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	return httprate.Limit(limit, window, httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.Identity.UserID == "" {
			return "", errors.New("authenticated user is missing")
		}
		return principal.Identity.InstitutionID + ":" + principal.Identity.UserID, nil
	}), httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests", nil)
	}), httprate.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
		slog.ErrorContext(r.Context(), "user rate-limit middleware failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to apply request rate limit", nil)
	}))
}
