package config

import (
	"testing"
)

func TestLoadRequiresDatabaseAndOrigin(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("ALLOWED_ORIGIN", "")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("CODE_HMAC_SECRET", "0123456789abcdef0123456789abcdef")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want missing DATABASE_URL")
	}
}

func TestLoadValidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("ALLOWED_ORIGIN", "https://web.example")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9000")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("CODE_HMAC_SECRET", "abcdef0123456789abcdef0123456789")
	t.Setenv("COOKIE_SAME_SITE", "lax")
	t.Setenv("COOKIE_SECURE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if len(cfg.JWTSecret) != 32 || len(cfg.CodeHMACSecret) != 32 {
		t.Fatal("secrets not loaded")
	}
}
