package ratelimit

import (
	"context"
	"testing"
	"time"

	redismock "github.com/go-redis/redismock/v9"
)

// TestRedisFixedWindowLimiter verifies Redis script responses are mapped into limiter decisions.
func TestRedisFixedWindowLimiter(t *testing.T) {
	client, mock := redismock.NewClientMock()
	limiter := NewRedisFixedWindowLimiter(client, time.Minute, "ratelimit")
	limiter.now = func() time.Time {
		return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	}
	redisKey := limiter.redisKey("token-hash")

	mock.ExpectEval(fixedWindowScript, []string{redisKey}, 1, 60).SetVal([]any{int64(1), int64(1), int64(60)})
	first, err := limiter.Allow(context.Background(), "token-hash", 1)
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if !first.Allowed {
		t.Fatal("expected first request to be allowed")
	}

	mock.ExpectEval(fixedWindowScript, []string{redisKey}, 1, 60).SetVal([]any{int64(0), int64(2), int64(59)})
	second, err := limiter.Allow(context.Background(), "token-hash", 1)
	if err != nil {
		t.Fatalf("second allow: %v", err)
	}
	if second.Allowed {
		t.Fatal("expected second request to be denied")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}
