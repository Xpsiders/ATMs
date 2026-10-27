package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr             string
	DatabaseURL          string
	AllowedOrigins       []string
	LogLevel             slog.Level
	JWTSecret            []byte
	CodeHMACSecret       []byte
	CookieSameSite       http.SameSite
	CookieSecure         bool
	MailAPIKey           string
	MailFrom             string
	AppEnv               string
	DeviceOfflineSeconds int
	// CheckinCodeWindow is how long a rotating session code stays valid. It is
	// the main limit on a student forwarding the code to an absent friend, so
	// it is kept short; the scanner, not a longer window, is what tolerates a
	// slow connection.
	CheckinCodeWindow time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:       httpAddr(),
		DatabaseURL:    strings.TrimSpace(os.Getenv("DATABASE_URL")),
		AllowedOrigins: parseOrigins(os.Getenv("ALLOWED_ORIGIN")),
		JWTSecret:      []byte(strings.TrimSpace(os.Getenv("JWT_SECRET"))),
		CodeHMACSecret: []byte(strings.TrimSpace(os.Getenv("CODE_HMAC_SECRET"))),
		MailAPIKey:     strings.TrimSpace(os.Getenv("MAIL_API_KEY")),
		MailFrom:       strings.TrimSpace(os.Getenv("MAIL_FROM")),
		AppEnv:         envOr("APP_ENV", "development"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if len(cfg.AllowedOrigins) == 0 {
		return Config{}, errors.New("ALLOWED_ORIGIN is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, errors.New("JWT_SECRET must contain at least 32 bytes")
	}
	if len(cfg.CodeHMACSecret) < 32 {
		return Config{}, errors.New("CODE_HMAC_SECRET must contain at least 32 bytes")
	}
	cookieSecure, err := strconv.ParseBool(envOr("COOKIE_SECURE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid COOKIE_SECURE: %w", err)
	}
	cfg.CookieSecure = cookieSecure
	switch strings.ToLower(envOr("COOKIE_SAME_SITE", "lax")) {
	case "lax":
		cfg.CookieSameSite = http.SameSiteLaxMode
	case "strict":
		cfg.CookieSameSite = http.SameSiteStrictMode
	case "none":
		cfg.CookieSameSite = http.SameSiteNoneMode
		if !cfg.CookieSecure {
			return Config{}, errors.New("COOKIE_SECURE must be true when COOKIE_SAME_SITE is none")
		}
	default:
		return Config{}, errors.New("COOKIE_SAME_SITE must be lax, strict, or none")
	}
	if cfg.MailAPIKey != "" && cfg.MailFrom == "" {
		return Config{}, errors.New("MAIL_FROM is required when MAIL_API_KEY is configured")
	}
	if cfg.AppEnv != "development" && cfg.AppEnv != "test" && cfg.AppEnv != "production" {
		return Config{}, errors.New("APP_ENV must be development, test, or production")
	}
	if cfg.MailAPIKey == "" && cfg.AppEnv != "development" {
		return Config{}, errors.New("MAIL_API_KEY is required outside development")
	}
	deviceOfflineSeconds, err := strconv.Atoi(envOr("DEVICE_OFFLINE_SECONDS", "60"))
	if err != nil || deviceOfflineSeconds <= 0 {
		return Config{}, errors.New("DEVICE_OFFLINE_SECONDS must be a positive integer")
	}
	cfg.DeviceOfflineSeconds = deviceOfflineSeconds

	codeWindowSeconds, err := strconv.Atoi(envOr("CHECKIN_CODE_WINDOW_SECONDS", "30"))
	if err != nil || codeWindowSeconds < 10 || codeWindowSeconds > 300 {
		return Config{}, errors.New("CHECKIN_CODE_WINDOW_SECONDS must be between 10 and 300")
	}
	cfg.CheckinCodeWindow = time.Duration(codeWindowSeconds) * time.Second

	switch strings.ToLower(envOr("LOG_LEVEL", "info")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn", "warning":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("invalid LOG_LEVEL %q", os.Getenv("LOG_LEVEL"))
	}
	return cfg, nil
}

// parseOrigins splits a comma-separated ALLOWED_ORIGIN into a trimmed, de-duped
// list of allowed CORS origins. An entry may be an exact origin
// (https://app.example) or a single-wildcard pattern (https://*.vercel.app) that
// matches any subdomain, so Vercel preview URLs can be allowed without a redeploy.
func parseOrigins(raw string) []string {
	seen := map[string]struct{}{}
	origins := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		origin := strings.TrimRight(strings.TrimSpace(part), "/")
		if origin == "" {
			continue
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins
}

func envOr(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

// httpAddr prefers the PORT variable that hosts such as Render inject and expect
// the service to bind, falling back to HTTP_ADDR and then the local default.
func httpAddr() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return ":" + port
	}
	return envOr("HTTP_ADDR", ":8080")
}
