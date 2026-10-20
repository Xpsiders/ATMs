package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr             string
	DatabaseURL          string
	AllowedOrigin        string
	LogLevel             slog.Level
	JWTSecret            []byte
	CodeHMACSecret       []byte
	CookieSameSite       http.SameSite
	CookieSecure         bool
	MailAPIKey           string
	MailFrom             string
	DeviceOfflineSeconds int
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:       envOr("HTTP_ADDR", ":8080"),
		DatabaseURL:    strings.TrimSpace(os.Getenv("DATABASE_URL")),
		AllowedOrigin:  strings.TrimSpace(os.Getenv("ALLOWED_ORIGIN")),
		JWTSecret:      []byte(strings.TrimSpace(os.Getenv("JWT_SECRET"))),
		CodeHMACSecret: []byte(strings.TrimSpace(os.Getenv("CODE_HMAC_SECRET"))),
		MailAPIKey:     strings.TrimSpace(os.Getenv("MAIL_API_KEY")),
		MailFrom:       strings.TrimSpace(os.Getenv("MAIL_FROM")),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.AllowedOrigin == "" {
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
	deviceOfflineSeconds, err := strconv.Atoi(envOr("DEVICE_OFFLINE_SECONDS", "60"))
	if err != nil || deviceOfflineSeconds <= 0 {
		return Config{}, errors.New("DEVICE_OFFLINE_SECONDS must be a positive integer")
	}
	cfg.DeviceOfflineSeconds = deviceOfflineSeconds

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

func envOr(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
