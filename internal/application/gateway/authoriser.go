// Package gateway contains the application service that authorises proxied requests.
package gateway

import (
	"context"
	"time"

	"github.com/maggie44/api-gateway/internal/domain/ratelimit"
	"github.com/maggie44/api-gateway/internal/domain/token"
)

// AuthorisationResult contains the token record and limiter decision for an admitted request.
type AuthorisationResult struct {
	TokenRecord token.Record
	Decision    ratelimit.Decision
}

// Authoriser coordinates token validation, route authorisation, and rate limiting.
type Authoriser struct {
	repository token.Repository
	limiter    ratelimit.Limiter
	now        func() time.Time
}

// NewAuthoriser constructs the application service that coordinates token lookup,
// authorisation checks, and rate-limit decisions.
func NewAuthoriser(repository token.Repository, limiter ratelimit.Limiter, now func() time.Time) *Authoriser {
	return &Authoriser{
		repository: repository,
		limiter:    limiter,
		now:        now,
	}
}

// Authorise validates the token record for the request and asks the limiter for admission.
func (a *Authoriser) Authorise(ctx context.Context, hashedAPIKey string, requestPath string) (AuthorisationResult, error) {
	// A small in-memory cache with a TTL of around one second could sit in front of this
	// Redis lookup to reduce token-read pressure during bursts. The trade-off is that
	// token expiry, revocation, or route-policy changes could take up to that cache TTL
	// to become visible on a given gateway instance, so the operational cost of slightly
	// stale authorisation data would need to be weighed against the latency and Redis-load
	// benefit before adopting that optimisation.
	record, err := a.repository.GetByHashedAPIKey(ctx, hashedAPIKey)
	if err != nil {
		return AuthorisationResult{}, err
	}

	if err := record.Validate(hashedAPIKey, a.now(), requestPath); err != nil {
		return AuthorisationResult{}, err
	}

	decision, err := a.limiter.Allow(ctx, hashedAPIKey, record.RateLimit)
	if err != nil {
		return AuthorisationResult{}, err
	}

	if !decision.Allowed {
		return AuthorisationResult{TokenRecord: record, Decision: decision}, ratelimit.ErrLimitExceeded
	}

	return AuthorisationResult{
		TokenRecord: record,
		Decision:    decision,
	}, nil
}
