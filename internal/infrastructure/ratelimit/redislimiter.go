// Package ratelimit provides infrastructure adapters for the rate-limit domain.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	domainratelimit "github.com/maggie44/api-gateway/internal/domain/ratelimit"
)

// fixedWindowScript keeps the counter increment, first-write expiry, and TTL read
// in one atomic Redis operation. Without Lua, separate INCR and EXPIRE calls can
// fail in between and leave a rate-limit key without expiry. A future refinement
// could wrap this with redis.NewScript(...) so Redis can cache the script by SHA,
// but the inline script keeps the limiter behaviour easier to read and reason
// about at the point of use.
const fixedWindowScript = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local current = redis.call("INCR", key)
if current == 1 then
  redis.call("EXPIRE", key, ttl)
end
local currentTTL = redis.call("TTL", key)
if current > limit then
  return {0, current, currentTTL}
end
return {1, current, currentTTL}
`

// RedisFixedWindowLimiter implements the domain limiter contract using Redis counters.
type RedisFixedWindowLimiter struct {
	client    *redis.Client
	window    time.Duration
	keyPrefix string
	now       func() time.Time
}

// NewRedisFixedWindowLimiter creates a Redis-backed fixed-window rate limiter.
func NewRedisFixedWindowLimiter(client *redis.Client, window time.Duration, keyPrefix string) *RedisFixedWindowLimiter {
	return &RedisFixedWindowLimiter{
		client:    client,
		window:    window,
		keyPrefix: keyPrefix,
		now:       time.Now,
	}
}

// Allow increments the current window counter in Redis and returns the resulting decision.
func (l *RedisFixedWindowLimiter) Allow(ctx context.Context, key string, limit int) (domainratelimit.Decision, error) {
	windowSeconds := int(l.window / time.Second)
	windowSeconds = max(windowSeconds, 1)

	// The script returns three values:
	// 1. whether the request is still within limit
	// 2. the current counter value for this window
	// 3. the key TTL so Retry-After can be derived without a second Redis call
	response, err := l.client.Eval(
		ctx,
		fixedWindowScript,
		[]string{l.redisKey(key)},
		limit,
		windowSeconds,
	).Result()
	if err != nil {
		return domainratelimit.Decision{}, fmt.Errorf("apply rate limit: %w", err)
	}

	values, ok := response.([]any)
	if !ok || len(values) != 3 {
		return domainratelimit.Decision{}, fmt.Errorf("unexpected rate limiter response %T", response)
	}

	allowed, err := toInt(values[0])
	if err != nil {
		return domainratelimit.Decision{}, err
	}
	current, err := toInt(values[1])
	if err != nil {
		return domainratelimit.Decision{}, err
	}
	ttl, err := toInt(values[2])
	if err != nil {
		return domainratelimit.Decision{}, err
	}

	return domainratelimit.Decision{
		Allowed:    allowed == 1,
		Limit:      limit,
		Current:    current,
		RetryAfter: time.Duration(ttl) * time.Second,
	}, nil
}

// redisKey builds the Redis key for the current rate-limit window and token hash.
func (l *RedisFixedWindowLimiter) redisKey(key string) string {
	// Dividing Unix time by the window size collapses all requests in the same
	// fixed time bucket onto one Redis key, for example one key per minute when
	// RATE_LIMIT_WINDOW=1m.
	windowID := l.now().UTC().Unix() / int64(max(int(l.window/time.Second), 1))
	return fmt.Sprintf("%s:%d:%s", l.keyPrefix, windowID, key)
}

// toInt converts Redis script return values into plain ints.
func toInt(value any) (int, error) {
	switch typed := value.(type) {
	case int64:
		return int(typed), nil
	case int:
		return typed, nil
	default:
		return 0, fmt.Errorf("unexpected rate limiter value type %T", value)
	}
}
