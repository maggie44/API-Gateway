// Package ratelimit defines the domain model for request admission decisions.
package ratelimit

import (
	"context"
	"errors"
	"time"
)

// ErrLimitExceeded reports that a token has exhausted its quota for the current window.
var ErrLimitExceeded = errors.New("rate limit exceeded")

// Decision describes the result of asking a limiter whether a request may proceed.
type Decision struct {
	Allowed    bool
	Limit      int
	Current    int
	RetryAfter time.Duration
}

// Limiter decides whether a request identified by key may proceed.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int) (Decision, error)
}
