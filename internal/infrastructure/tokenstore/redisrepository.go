// Package tokenstore provides infrastructure adapters for token persistence.
package tokenstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/maggie44/api-gateway/internal/domain/token"
)

// RedisRepository implements token.Repository using go-redis.
type RedisRepository struct {
	client    *redis.Client
	keyPrefix string
}

// NewRedisRepository creates the Redis-backed token repository implementation.
func NewRedisRepository(client *redis.Client, keyPrefix string) *RedisRepository {
	return &RedisRepository{
		client:    client,
		keyPrefix: keyPrefix,
	}
}

// GetByHashedAPIKey reads a token record from Redis using the hashed token identifier.
func (r *RedisRepository) GetByHashedAPIKey(ctx context.Context, hashedAPIKey string) (token.Record, error) {
	// Token records are stored as JSON so the Redis payload shape stays easy to
	// inspect during local demos and mirrors the token record structure directly.
	recordJSON, err := r.client.Get(ctx, r.key(hashedAPIKey)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return token.Record{}, token.ErrNotFound
		}
		return token.Record{}, fmt.Errorf("get token: %w", err)
	}

	var record token.Record
	if err := json.Unmarshal([]byte(recordJSON), &record); err != nil {
		return token.Record{}, fmt.Errorf("decode token payload: %w", err)
	}

	return record, nil
}

// Put stores a token record in Redis under its hashed token identifier.
func (r *RedisRepository) Put(ctx context.Context, hashedAPIKey string, record token.Record) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal token payload: %w", err)
	}

	if err := r.client.Set(ctx, r.key(hashedAPIKey), payload, 0).Err(); err != nil {
		return fmt.Errorf("set token payload: %w", err)
	}

	return nil
}

// key formats the Redis storage key for a hashed token identifier.
func (r *RedisRepository) key(hashedAPIKey string) string {
	// The hashed token lives both in the key and in the JSON payload so the lookup
	// is efficient while the stored record still preserves the expected token shape.
	return fmt.Sprintf("%s:%s", r.keyPrefix, hashedAPIKey)
}
