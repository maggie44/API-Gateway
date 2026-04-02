// Package token defines the core token authorisation model used by the gateway.
package token

import (
	"context"
	"crypto/subtle"
	"errors"
	"path"
	"strings"
	"time"
)

var (
	// ErrNotFound indicates that no token record exists for the supplied hash.
	ErrNotFound = errors.New("token not found")
	// ErrExpired indicates that the token record is past its expiry time.
	ErrExpired = errors.New("token expired")
	// ErrRouteNotAllowed indicates that the request path is outside the token policy.
	ErrRouteNotAllowed = errors.New("route not allowed")
	// ErrHashMismatch indicates that the stored token hash does not match the presented token hash.
	ErrHashMismatch = errors.New("token hash mismatch")
)

// Record is the domain representation of a token entry loaded from Redis.
type Record struct {
	APIKey        string    `json:"api_key"`
	RateLimit     int       `json:"rate_limit"`
	ExpiresAt     time.Time `json:"expires_at"`
	AllowedRoutes []string  `json:"allowed_routes"`
}

// Repository provides persistence operations for token records.
type Repository interface {
	GetByHashedAPIKey(ctx context.Context, hashedAPIKey string) (Record, error)
	Put(ctx context.Context, hashedAPIKey string, record Record) error
}

// Validate enforces hash equality, expiry, and route authorisation for a token record.
func (r Record) Validate(expectedHash string, now time.Time, requestPath string) error {
	if subtle.ConstantTimeCompare([]byte(r.APIKey), []byte(expectedHash)) != 1 {
		return ErrHashMismatch
	}

	if !r.ExpiresAt.After(now.UTC()) {
		return ErrExpired
	}

	if !r.Allows(requestPath) {
		return ErrRouteNotAllowed
	}

	return nil
}

// Allows reports whether the request path matches any allowed route pattern on the record.
// In a high-throughput production system the allowed routes could be pre-normalised at
// load time so this loop only performs cheap string comparisons instead of cleaning each
// route on every request. At realistic allowed-route counts this is negligible.
func (r Record) Allows(requestPath string) bool {
	cleanPath := normaliseRoute(requestPath)

	for _, allowedRoute := range r.AllowedRoutes {
		normalised := normaliseRoute(allowedRoute)
		if base, ok := strings.CutSuffix(normalised, "/*"); ok {
			if cleanPath == base || strings.HasPrefix(cleanPath, base+"/") {
				return true
			}
			continue
		}

		if cleanPath == normalised {
			return true
		}
	}

	return false
}

// normaliseRoute converts route patterns and request paths into a stable format for comparisons.
func normaliseRoute(route string) string {
	trimmed := strings.TrimSpace(route)
	if trimmed == "" || trimmed == "/" {
		return "/"
	}

	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}

	return strings.TrimRight(path.Clean(trimmed), "/")
}
