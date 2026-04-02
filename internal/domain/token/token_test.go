package token

import (
	"testing"
	"time"
)

// TestHashAPIKey verifies deterministic hashing and secret sensitivity.
func TestHashAPIKey(t *testing.T) {
	first := HashAPIKey("demo-token", "secret")
	second := HashAPIKey("demo-token", "secret")
	third := HashAPIKey("demo-token", "other-secret")

	if first != second {
		t.Fatal("expected stable hash for same input")
	}

	if first == third {
		t.Fatal("expected different secrets to produce different hashes")
	}
}

// TestRecordAllows verifies route wildcard matching on token records.
func TestRecordAllows(t *testing.T) {
	record := Record{
		AllowedRoutes: []string{"/api/v1/users/*", "/api/v1/products/*"},
	}

	if !record.Allows("/api/v1/users/123") {
		t.Fatal("expected users path to be allowed")
	}

	if record.Allows("/api/v1/orders/123") {
		t.Fatal("expected orders path to be denied")
	}
}

// TestRecordValidate verifies the main token validation branches.
func TestRecordValidate(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	record := Record{
		APIKey:        "hashed-token",
		RateLimit:     5,
		ExpiresAt:     now.Add(time.Minute),
		AllowedRoutes: []string{"/api/v1/users/*"},
	}

	tests := []struct {
		name         string
		expectedHash string
		now          time.Time
		requestPath  string
		wantErr      error
	}{
		{"valid token", "hashed-token", now, "/api/v1/users/1", nil},
		{"hash mismatch", "other-token", now, "/api/v1/users/1", ErrHashMismatch},
		{"expired token", "hashed-token", now.Add(2 * time.Minute), "/api/v1/users/1", ErrExpired},
		{"route not allowed", "hashed-token", now, "/api/v1/products/1", ErrRouteNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := record.Validate(tt.expectedHash, tt.now, tt.requestPath)
			if err != tt.wantErr {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestNormaliseRoute verifies edge cases in route normalisation.
func TestNormaliseRoute(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", "/"},
		{"root path", "/", "/"},
		{"no leading slash", "api/v1/users", "/api/v1/users"},
		{"trailing slash", "/api/v1/users/", "/api/v1/users"},
		{"whitespace only", "   ", "/"},
		{"surrounding whitespace", "  /api/v1/users  ", "/api/v1/users"},
		{"dot segment", "/api/v1/users/../products", "/api/v1/products"},
		{"duplicate slashes", "/api/v1/users//123", "/api/v1/users/123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normaliseRoute(tt.input)
			if got != tt.want {
				t.Fatalf("normaliseRoute(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
