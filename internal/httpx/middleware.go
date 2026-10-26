package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

type requestIDKey struct{}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if !validRequestID(id) {
			var bytes [16]byte
			if _, err := rand.Read(bytes[:]); err != nil {
				WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to create request ID", nil)
				return
			}
			id = hex.EncodeToString(bytes[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := originMatcher(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			permitted := origin != "" && allowed(origin)
			if permitted {
				// Echo the matched origin (never "*") so credentialed requests are
				// allowed, and Vary so caches key on Origin.
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Device-Key, X-Request-ID, If-None-Match")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			}
			if r.Method == http.MethodOptions {
				if !permitted {
					WriteError(w, http.StatusForbidden, "cors_origin_denied", "Origin is not allowed", nil)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// originMatcher builds a predicate that permits an Origin when it exactly equals
// an allowed entry, or matches a single-wildcard entry such as
// "https://*.vercel.app" (any subdomain). Only allowlisted origins are ever
// reflected back; arbitrary origins are never echoed.
func originMatcher(allowedOrigins []string) func(string) bool {
	type wildcard struct{ prefix, suffix string }
	exact := make(map[string]struct{})
	var wildcards []wildcard
	for _, entry := range allowedOrigins {
		entry = strings.TrimRight(strings.TrimSpace(entry), "/")
		if entry == "" {
			continue
		}
		if index := strings.IndexByte(entry, '*'); index >= 0 {
			wildcards = append(wildcards, wildcard{prefix: entry[:index], suffix: entry[index+1:]})
			continue
		}
		exact[entry] = struct{}{}
	}
	return func(origin string) bool {
		origin = strings.TrimRight(origin, "/")
		if _, ok := exact[origin]; ok {
			return true
		}
		for _, w := range wildcards {
			if len(origin) > len(w.prefix)+len(w.suffix) &&
				strings.HasPrefix(origin, w.prefix) && strings.HasSuffix(origin, w.suffix) {
				return true
			}
		}
		return false
	}
}
