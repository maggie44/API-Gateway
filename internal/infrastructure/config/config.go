// Package config loads environment-driven configuration for the gateway and local tooling.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Route maps a path prefix to an upstream target URL.
type Route struct {
	PathPrefix string
	Target     *url.URL
}

// Config contains all runtime settings required by the gateway service.
type Config struct {
	ListenAddress      string
	LogLevel           string
	RedisAddress       string
	RedisPassword      string
	RedisDB            int
	RedisTimeout       time.Duration
	ProxyTimeout       time.Duration
	RateLimitWindow    time.Duration
	TokenHashSecret    string
	TokenKeyPrefix     string
	RateLimitKeyPrefix string
	Routes             []Route
}

const (
	defaultListenAddress      = ":8080"
	defaultRedisAddress       = "127.0.0.1:6379"
	defaultRedisTimeout       = 3 * time.Second
	defaultProxyTimeout       = 15 * time.Second
	defaultRateLimitWindow    = time.Minute
	defaultTokenKeyPrefix     = "token"
	defaultRateLimitKeyPrefix = "ratelimit"
)

var (
	// ErrMissingTokenHashSecret reports that the token hashing secret was not configured.
	ErrMissingTokenHashSecret = errors.New("TOKEN_HASH_SECRET is required")
	// ErrMissingRoutes reports that no upstream route mappings were configured.
	ErrMissingRoutes = errors.New("ROUTES is required")
)

// envResolver merges file-sourced .env values with process environment variables.
// Process env vars always take precedence, so .env acts as a defaults layer
// without mutating global process state.
type envResolver struct {
	fileEnv map[string]string
}

// newEnvResolver builds a resolver backed by the .env file map produced by LoadDotEnv.
func newEnvResolver(fileEnv map[string]string) envResolver {
	return envResolver{fileEnv: fileEnv}
}

// get returns the value for key, preferring the process environment over the file map.
func (r envResolver) get(key string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return r.fileEnv[key]
}

// getDefault returns the value for key when present, otherwise the fallback.
func (r envResolver) getDefault(key, fallback string) string {
	if value := r.get(key); value != "" {
		return value
	}
	return fallback
}

// Load reads the gateway configuration from environment variables, optionally
// preloading values from a local .env file when present.
func Load() (Config, error) {
	// A future production deployment could replace .env loading here with a secrets
	// manager integration, for example AWS Secrets Manager or SSM Parameter Store,
	// while keeping the rest of the configuration flow unchanged.
	fileEnv, err := LoadDotEnv(".env")
	if err != nil {
		return Config{}, err
	}
	env := newEnvResolver(fileEnv)

	redisTimeout, err := parseDuration(env.get("REDIS_TIMEOUT"), defaultRedisTimeout)
	if err != nil {
		return Config{}, fmt.Errorf("parse REDIS_TIMEOUT: %w", err)
	}

	proxyTimeout, err := parseDuration(env.get("PROXY_TIMEOUT"), defaultProxyTimeout)
	if err != nil {
		return Config{}, fmt.Errorf("parse PROXY_TIMEOUT: %w", err)
	}

	rateLimitWindow, err := parseDuration(env.get("RATE_LIMIT_WINDOW"), defaultRateLimitWindow)
	if err != nil {
		return Config{}, fmt.Errorf("parse RATE_LIMIT_WINDOW: %w", err)
	}

	cfg := Config{
		ListenAddress:      env.getDefault("LISTEN_ADDRESS", defaultListenAddress),
		LogLevel:           strings.ToLower(env.getDefault("LOG_LEVEL", "info")),
		RedisAddress:       env.getDefault("REDIS_ADDRESS", defaultRedisAddress),
		RedisPassword:      env.get("REDIS_PASSWORD"),
		RedisTimeout:       redisTimeout,
		ProxyTimeout:       proxyTimeout,
		RateLimitWindow:    rateLimitWindow,
		TokenHashSecret:    env.get("TOKEN_HASH_SECRET"),
		TokenKeyPrefix:     env.getDefault("TOKEN_KEY_PREFIX", defaultTokenKeyPrefix),
		RateLimitKeyPrefix: env.getDefault("RATE_LIMIT_KEY_PREFIX", defaultRateLimitKeyPrefix),
	}

	db, err := strconv.Atoi(env.getDefault("REDIS_DB", "0"))
	if err != nil {
		return Config{}, fmt.Errorf("parse REDIS_DB: %w", err)
	}
	cfg.RedisDB = db

	routes, err := parseRoutes(env.get("ROUTES"))
	if err != nil {
		return Config{}, err
	}
	cfg.Routes = routes

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// SeedConfig contains the settings required by the static token seed command.
type SeedConfig struct {
	RedisAddress    string
	RedisPassword   string
	RedisDB         int
	RedisTimeout    time.Duration
	TokenHashSecret string
	TokenKeyPrefix  string
	SeedFilePath    string
}

// LoadSeedConfig reads the environment-driven configuration used by the token seed command.
func LoadSeedConfig() (SeedConfig, error) {
	fileEnv, err := LoadDotEnv(".env")
	if err != nil {
		return SeedConfig{}, err
	}
	env := newEnvResolver(fileEnv)

	redisTimeout, err := parseDuration(env.get("REDIS_TIMEOUT"), defaultRedisTimeout)
	if err != nil {
		return SeedConfig{}, fmt.Errorf("parse REDIS_TIMEOUT: %w", err)
	}

	cfg := SeedConfig{
		RedisAddress:    env.getDefault("REDIS_ADDRESS", defaultRedisAddress),
		RedisPassword:   env.get("REDIS_PASSWORD"),
		RedisTimeout:    redisTimeout,
		TokenHashSecret: env.get("TOKEN_HASH_SECRET"),
		TokenKeyPrefix:  env.getDefault("TOKEN_KEY_PREFIX", defaultTokenKeyPrefix),
		SeedFilePath:    env.getDefault("STATIC_TOKEN_FILE", "development/infrastructure/static-tokens.json"),
	}

	db, err := strconv.Atoi(env.getDefault("REDIS_DB", "0"))
	if err != nil {
		return SeedConfig{}, fmt.Errorf("parse REDIS_DB: %w", err)
	}
	cfg.RedisDB = db

	if cfg.TokenHashSecret == "" {
		return SeedConfig{}, ErrMissingTokenHashSecret
	}

	return cfg, nil
}

// Validate checks that the minimum required gateway configuration is present.
func (c Config) Validate() error {
	if c.TokenHashSecret == "" {
		return ErrMissingTokenHashSecret
	}

	if len(c.Routes) == 0 {
		return ErrMissingRoutes
	}

	return nil
}

// LoadDotEnv reads simple KEY=VALUE pairs from a .env file and returns them as a
// map without mutating the process environment. When the file does not exist the
// returned map is empty.
func LoadDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open env file: %w", err)
	}

	result := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			_ = file.Close()
			return nil, fmt.Errorf("invalid env line %q", line)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			_ = file.Close()
			return nil, fmt.Errorf("invalid env key in line %q", line)
		}

		result[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}

	if err := scanner.Err(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("scan env file: %w", err)
	}

	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close env file: %w", err)
	}

	return result, nil
}

// parseRoutes converts the ROUTES environment variable into sorted path-prefix mappings.
func parseRoutes(value string) ([]Route, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	entries := strings.Split(value, ",")
	routes := make([]Route, 0, len(entries))
	for _, entry := range entries {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid route %q", entry)
		}

		prefix := normalisePrefix(parts[0])
		target, err := url.Parse(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("parse route target for %q: %w", prefix, err)
		}

		if target.Scheme == "" || target.Host == "" {
			return nil, fmt.Errorf("route target for %q must include scheme and host", prefix)
		}

		routes = append(routes, Route{
			PathPrefix: prefix,
			Target:     target,
		})
	}

	sort.Slice(routes, func(i, j int) bool {
		// Match the most specific prefix first so a route like /api/v1/users/admin
		// wins over a broader prefix such as /api/v1/users.
		return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
	})

	return routes, nil
}

// normalisePrefix ensures route prefixes are compared in a consistent format.
func normalisePrefix(prefix string) string {
	normalised := strings.TrimSpace(prefix)
	if !strings.HasPrefix(normalised, "/") {
		normalised = "/" + normalised
	}

	return strings.TrimRight(normalised, "/")
}

// parseDuration parses a Go duration string and returns the fallback when the
// value is empty. A non-empty value that cannot be parsed is treated as a
// configuration error so typos like "1xm" fail fast instead of silently falling
// back to the default.
func parseDuration(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}

	return duration, nil
}
