// Command token-seed loads static token definitions into Redis for local development.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/maggie44/api-gateway/internal/domain/token"
	"github.com/maggie44/api-gateway/internal/infrastructure/config"
	"github.com/maggie44/api-gateway/internal/infrastructure/tokenstore"
)

type staticTokenSeed struct {
	Token         string   `json:"token"`
	RateLimit     int      `json:"rate_limit"`
	ExpiresAt     string   `json:"expires_at"`
	AllowedRoutes []string `json:"allowed_routes"`
}

// main delegates to run so deferred cleanup can complete before the process exits.
func main() {
	if err := run(); err != nil {
		slog.Error("token seed exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run loads static token definitions from the environment-configured file and
// writes their hashed records into Redis for local development.
func run() error {
	cfg, err := config.LoadSeedConfig()
	if err != nil {
		return fmt.Errorf("load seed config: %w", err)
	}

	payload, err := os.ReadFile(cfg.SeedFilePath)
	if err != nil {
		return fmt.Errorf("read seed file: %w", err)
	}

	var seeds []staticTokenSeed
	if err := json.Unmarshal(payload, &seeds); err != nil {
		return fmt.Errorf("decode seed file: %w", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddress,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		DialTimeout:  cfg.RedisTimeout,
		ReadTimeout:  cfg.RedisTimeout,
		WriteTimeout: cfg.RedisTimeout,
	})
	repository := tokenstore.NewRedisRepository(client, cfg.TokenKeyPrefix)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, seed := range seeds {
		expiresAt, err := time.Parse(time.RFC3339, seed.ExpiresAt)
		if err != nil {
			return fmt.Errorf("parse expires_at for %q: %w", seed.Token, err)
		}

		hashed := token.HashAPIKey(seed.Token, cfg.TokenHashSecret)
		record := token.Record{
			APIKey:        hashed,
			RateLimit:     seed.RateLimit,
			ExpiresAt:     expiresAt.UTC(),
			AllowedRoutes: seed.AllowedRoutes,
		}

		if err := repository.Put(ctx, hashed, record); err != nil {
			return fmt.Errorf("seed token %q: %w", seed.Token, err)
		}

		slog.Info("seeded static token",
			slog.Time("expires_at", expiresAt.UTC()),
			slog.Int("allowed_route_count", len(seed.AllowedRoutes)),
		)
	}

	return nil
}
