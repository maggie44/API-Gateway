package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maggie44/api-gateway/internal/domain/ratelimit"
	"github.com/maggie44/api-gateway/internal/domain/token"
)

type stubRepository struct {
	record token.Record
	err    error
}

// GetByHashedAPIKey returns the configured test record for authoriser unit tests.
func (s stubRepository) GetByHashedAPIKey(_ context.Context, _ string) (token.Record, error) {
	return s.record, s.err
}

// Put satisfies the repository interface for tests that never write records.
func (s stubRepository) Put(_ context.Context, _ string, _ token.Record) error {
	return nil
}

type stubLimiter struct {
	decision ratelimit.Decision
	err      error
}

// Allow returns the configured rate-limit decision for authoriser unit tests.
func (s stubLimiter) Allow(_ context.Context, _ string, _ int) (ratelimit.Decision, error) {
	return s.decision, s.err
}

// TestAuthorise verifies the authorisation flow across success and failure branches.
func TestAuthorise(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	validRecord := token.Record{
		APIKey:        "hashed-token",
		RateLimit:     2,
		ExpiresAt:     now.Add(time.Minute),
		AllowedRoutes: []string{"/api/v1/users/*"},
	}

	tests := []struct {
		name       string
		repository stubRepository
		limiter    stubLimiter
		hash       string
		path       string
		wantErr    error
	}{
		{
			name:       "successful authorisation",
			repository: stubRepository{record: validRecord},
			limiter:    stubLimiter{decision: ratelimit.Decision{Allowed: true, Limit: 2, Current: 1}},
			hash:       "hashed-token",
			path:       "/api/v1/users/1",
			wantErr:    nil,
		},
		{
			name:       "token not found",
			repository: stubRepository{err: token.ErrNotFound},
			limiter:    stubLimiter{},
			hash:       "missing-token",
			path:       "/api/v1/users/1",
			wantErr:    token.ErrNotFound,
		},
		{
			name:       "expired token",
			repository: stubRepository{record: token.Record{APIKey: "hashed-token", RateLimit: 2, ExpiresAt: now.Add(-time.Minute), AllowedRoutes: []string{"/api/v1/users/*"}}},
			limiter:    stubLimiter{},
			hash:       "hashed-token",
			path:       "/api/v1/users/1",
			wantErr:    token.ErrExpired,
		},
		{
			name:       "route not allowed",
			repository: stubRepository{record: validRecord},
			limiter:    stubLimiter{},
			hash:       "hashed-token",
			path:       "/api/v1/products/1",
			wantErr:    token.ErrRouteNotAllowed,
		},
		{
			name:       "rate limit exceeded",
			repository: stubRepository{record: validRecord},
			limiter:    stubLimiter{decision: ratelimit.Decision{Allowed: false, Limit: 1, Current: 2, RetryAfter: time.Second}},
			hash:       "hashed-token",
			path:       "/api/v1/users/1",
			wantErr:    ratelimit.ErrLimitExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authoriser := NewAuthoriser(tt.repository, tt.limiter, func() time.Time { return now })
			_, err := authoriser.Authorise(context.Background(), tt.hash, tt.path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// BenchmarkAuthoriseComparison runs both baseline and hash-inclusive variants in
// one benchmark so their results can be compared side-by-side in a single run.
// This is useful when evaluating hashing-related choices by showing the direct
// CPU and allocation impact of including token hashing in the hot path.
func BenchmarkAuthoriseComparison(b *testing.B) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	rawToken := "users-static-token"
	secret := "benchmark-secret"
	hashedToken := token.HashAPIKey(rawToken, secret)

	record := token.Record{
		APIKey:        hashedToken,
		RateLimit:     10,
		ExpiresAt:     now.Add(time.Hour),
		AllowedRoutes: []string{"/api/v1/users/*"},
	}

	authoriser := NewAuthoriser(
		stubRepository{record: record},
		stubLimiter{decision: ratelimit.Decision{Allowed: true, Limit: 10, Current: 1}},
		func() time.Time { return now },
	)

	b.Run("without_hash", func(b *testing.B) {
		b.ResetTimer()
		for b.Loop() {
			if _, err := authoriser.Authorise(ctx, hashedToken, "/api/v1/users/1"); err != nil {
				b.Fatalf("authorise without hash: %v", err)
			}
		}
	})

	b.Run("with_hash", func(b *testing.B) {
		b.ResetTimer()
		for b.Loop() {
			hash := token.HashAPIKey(rawToken, secret)
			if _, err := authoriser.Authorise(ctx, hash, "/api/v1/users/1"); err != nil {
				b.Fatalf("authorise with hash: %v", err)
			}
		}
	})
}
