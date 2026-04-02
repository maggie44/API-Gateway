package tokenstore

import (
	"context"
	"errors"
	"testing"

	redismock "github.com/go-redis/redismock/v9"

	"github.com/maggie44/api-gateway/internal/domain/token"
)

// TestRedisRepositoryGetByHashedAPIKey verifies the repository unmarshals Redis payloads correctly.
func TestRedisRepositoryGetByHashedAPIKey(t *testing.T) {
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("token:hash").SetVal(`{"api_key":"hash","rate_limit":5,"expires_at":"2027-12-31T23:59:59Z","allowed_routes":["/api/v1/users/*"]}`)

	repository := NewRedisRepository(client, "token")
	got, err := repository.GetByHashedAPIKey(context.Background(), "hash")
	if err != nil {
		t.Fatalf("get token: %v", err)
	}

	if got.APIKey != "hash" {
		t.Fatalf("expected hash, got %q", got.APIKey)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

// TestRedisRepositoryGetByHashedAPIKeyNotFound verifies Redis misses map to the domain not-found error.
func TestRedisRepositoryGetByHashedAPIKeyNotFound(t *testing.T) {
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("token:missing").RedisNil()

	repository := NewRedisRepository(client, "token")
	_, err := repository.GetByHashedAPIKey(context.Background(), "missing")
	if !errors.Is(err, token.ErrNotFound) {
		t.Fatalf("expected token not found, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

// TestRedisRepositoryGetByHashedAPIKeyRejectsInvalidJSON verifies corrupt Redis payloads fail decoding.
func TestRedisRepositoryGetByHashedAPIKeyRejectsInvalidJSON(t *testing.T) {
	client, mock := redismock.NewClientMock()
	mock.ExpectGet("token:hash").SetVal(`{"api_key":`)

	repository := NewRedisRepository(client, "token")
	if _, err := repository.GetByHashedAPIKey(context.Background(), "hash"); err == nil {
		t.Fatal("expected invalid JSON to fail")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}

// TestRedisRepositoryPut verifies token records are written back to Redis under the hashed key.
func TestRedisRepositoryPut(t *testing.T) {
	client, mock := redismock.NewClientMock()
	record := token.Record{
		APIKey:        "hash",
		RateLimit:     5,
		AllowedRoutes: []string{"/api/v1/users/*"},
	}
	mock.ExpectSet("token:hash", []byte(`{"api_key":"hash","rate_limit":5,"expires_at":"0001-01-01T00:00:00Z","allowed_routes":["/api/v1/users/*"]}`), 0).SetVal("OK")

	repository := NewRedisRepository(client, "token")
	if err := repository.Put(context.Background(), "hash", record); err != nil {
		t.Fatalf("put token: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("redis expectations: %v", err)
	}
}
