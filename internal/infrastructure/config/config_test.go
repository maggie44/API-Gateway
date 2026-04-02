package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestParseRoutes verifies route parsing from the ROUTES environment variable format.
func TestParseRoutes(t *testing.T) {
	routes, err := parseRoutes("/api/v1/users=http://localhost:8081,/api/v1/products=http://localhost:8082")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	if len(routes) != 2 {
		t.Fatalf("expected two routes, got %d", len(routes))
	}

	if routes[0].PathPrefix != "/api/v1/products" && routes[0].PathPrefix != "/api/v1/users" {
		t.Fatalf("unexpected route prefix %q", routes[0].PathPrefix)
	}
}

// TestLoadDotEnvRejectsMalformedLines verifies malformed .env content fails fast.
func TestLoadDotEnvRejectsMalformedLines(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envPath, []byte("BROKEN_LINE\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	if _, err := LoadDotEnv(envPath); err == nil {
		t.Fatal("expected malformed env file to return an error")
	}
}

// TestLoadDotEnvReturnsMapWithoutMutatingEnv verifies .env values are returned
// in a map rather than written to the process environment.
func TestLoadDotEnvReturnsMapWithoutMutatingEnv(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envPath, []byte("MY_TEST_KEY=my_value\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	result, err := LoadDotEnv(envPath)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	if result["MY_TEST_KEY"] != "my_value" {
		t.Fatalf("expected map to contain MY_TEST_KEY=my_value, got %q", result["MY_TEST_KEY"])
	}

	if os.Getenv("MY_TEST_KEY") != "" {
		t.Fatal("expected LoadDotEnv not to mutate process environment")
	}
}

// TestLoad verifies the gateway can load a complete environment configuration.
func TestLoad(t *testing.T) {
	tempDir := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}

	t.Cleanup(func() {
		if chdirErr := os.Chdir(previousDir); chdirErr != nil {
			t.Fatalf("restore working directory: %v", chdirErr)
		}
	})

	t.Setenv("TOKEN_HASH_SECRET", "secret")
	t.Setenv("ROUTES", "/api/v1/users=http://localhost:8081,/api/v1/users/admin=http://localhost:8082")
	t.Setenv("REDIS_DB", "2")
	t.Setenv("REDIS_TIMEOUT", "4s")
	t.Setenv("PROXY_TIMEOUT", "7s")
	t.Setenv("RATE_LIMIT_WINDOW", "2m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.TokenHashSecret != "secret" {
		t.Fatalf("expected token hash secret to load, got %q", cfg.TokenHashSecret)
	}

	if cfg.RedisDB != 2 {
		t.Fatalf("expected Redis DB 2, got %d", cfg.RedisDB)
	}

	if cfg.RedisTimeout != 4*time.Second {
		t.Fatalf("expected Redis timeout 4s, got %s", cfg.RedisTimeout)
	}

	if cfg.ProxyTimeout != 7*time.Second {
		t.Fatalf("expected proxy timeout 7s, got %s", cfg.ProxyTimeout)
	}

	if cfg.RateLimitWindow != 2*time.Minute {
		t.Fatalf("expected rate-limit window 2m, got %s", cfg.RateLimitWindow)
	}

	if len(cfg.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(cfg.Routes))
	}

	if cfg.Routes[0].PathPrefix != "/api/v1/users/admin" {
		t.Fatalf("expected most specific route first, got %q", cfg.Routes[0].PathPrefix)
	}
}

// TestLoadRejectsInvalidRedisDB verifies invalid Redis DB values fail fast.
func TestLoadRejectsInvalidRedisDB(t *testing.T) {
	tempDir := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}

	t.Cleanup(func() {
		if chdirErr := os.Chdir(previousDir); chdirErr != nil {
			t.Fatalf("restore working directory: %v", chdirErr)
		}
	})

	t.Setenv("TOKEN_HASH_SECRET", "secret")
	t.Setenv("ROUTES", "/api/v1/users=http://localhost:8081")
	t.Setenv("REDIS_DB", "not-a-number")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid Redis DB to fail")
	}
}

// TestLoadSeedConfigRequiresTokenHashSecret verifies the seed command enforces its auth secret dependency.
func TestLoadSeedConfigRequiresTokenHashSecret(t *testing.T) {
	tempDir := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}

	t.Cleanup(func() {
		if chdirErr := os.Chdir(previousDir); chdirErr != nil {
			t.Fatalf("restore working directory: %v", chdirErr)
		}
	})

	t.Setenv("TOKEN_HASH_SECRET", "")

	_, err = LoadSeedConfig()
	if !errors.Is(err, ErrMissingTokenHashSecret) {
		t.Fatalf("expected missing token hash secret error, got %v", err)
	}
}

// TestLoadRejectsInvalidDuration verifies that a malformed duration value fails
// fast instead of silently falling back to the default.
func TestLoadRejectsInvalidDuration(t *testing.T) {
	tempDir := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}

	t.Cleanup(func() {
		if chdirErr := os.Chdir(previousDir); chdirErr != nil {
			t.Fatalf("restore working directory: %v", chdirErr)
		}
	})

	t.Setenv("TOKEN_HASH_SECRET", "secret")
	t.Setenv("ROUTES", "/api/v1/users=http://localhost:8081")
	t.Setenv("RATE_LIMIT_WINDOW", "1xm")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid duration to fail")
	}
}
