package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/maggie44/api-gateway/internal/application/gateway"
	"github.com/maggie44/api-gateway/internal/domain/ratelimit"
	"github.com/maggie44/api-gateway/internal/domain/token"
	"github.com/maggie44/api-gateway/internal/infrastructure/config"
)

type stubPinger struct {
	err error
}

// Ping returns the configured Redis health result for readiness tests.
func (s stubPinger) Ping(context.Context) error {
	return s.err
}

// TestHealthAndReadyEndpointsWithoutAuth verifies operational endpoints stay public.
func TestHealthAndReadyEndpointsWithoutAuth(t *testing.T) {
	server := newTestServer(t, stubPinger{})

	healthResponse := httptest.NewRecorder()
	healthRequest := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodGet, "/healthz", nil)
	server.Router().ServeHTTP(healthResponse, healthRequest)
	if healthResponse.Code != stdhttp.StatusOK {
		t.Fatalf("expected healthz to return 200, got %d", healthResponse.Code)
	}

	readyResponse := httptest.NewRecorder()
	readyRequest := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodGet, "/readyz", nil)
	server.Router().ServeHTTP(readyResponse, readyRequest)
	if readyResponse.Code != stdhttp.StatusOK {
		t.Fatalf("expected readyz to return 200, got %d", readyResponse.Code)
	}
}

// TestProtectedRouteRequiresToken verifies protected routes reject missing credentials.
func TestProtectedRouteRequiresToken(t *testing.T) {
	server := newTestServer(t, stubPinger{})

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodPost, "/api/v1/users/123", nil)
	server.Router().ServeHTTP(response, request)

	if response.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

// TestProtectedRouteProxiesRequest verifies an authorised request is forwarded upstream
// with the expected path, host, and header transformations.
func TestProtectedRouteProxiesRequest(t *testing.T) {
	var captured struct {
		path            string
		host            string
		authorization   string
		correlationID   string
		connection      string
		removeMe        string
		forwardedFor    string
		forwardedHost   string
		forwardedProto  string
		forwardedHeader string
	}

	server := newServerWithUpstream(t, "http://users.example.internal", stubPinger{}, token.Record{
		APIKey:        token.HashAPIKey("users-static-token", "secret"),
		RateLimit:     5,
		ExpiresAt:     time.Now().Add(time.Hour),
		AllowedRoutes: []string{"/api/v1/users/*"},
	}, ratelimit.Decision{
		Allowed: true,
		Limit:   5,
		Current: 1,
	}, roundTripFunc(func(request *stdhttp.Request) (*stdhttp.Response, error) {
		captured.path = request.URL.Path
		captured.host = request.URL.Host
		captured.authorization = request.Header.Get("Authorization")
		captured.correlationID = request.Header.Get("X-Correlation-Id")
		captured.connection = request.Header.Get("Connection")
		captured.removeMe = request.Header.Get("X-Remove-Me")
		captured.forwardedFor = request.Header.Get("X-Forwarded-For")
		captured.forwardedHost = request.Header.Get("X-Forwarded-Host")
		captured.forwardedProto = request.Header.Get("X-Forwarded-Proto")
		captured.forwardedHeader = request.Header.Get("Forwarded")
		recorder := httptest.NewRecorder()
		recorder.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(recorder).Encode(map[string]string{"ok": "true"})
		return recorder.Result(), nil
	}))

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodPost, "/api/v1/users/123", strings.NewReader(`{}`))
	request.Host = "127.0.0.1:8080"
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Authorization", "Bearer users-static-token")
	request.Header.Set("X-Correlation-Id", "corr-123")
	request.Header.Set("Connection", "keep-alive, X-Remove-Me")
	request.Header.Set("X-Remove-Me", "secret-hop-by-hop-value")
	request.Header.Set("X-Forwarded-For", "1.2.3.4")
	request.Header.Set("X-Forwarded-Host", "spoofed.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Forwarded", "for=1.2.3.4;host=spoofed.example;proto=https")
	server.Router().ServeHTTP(response, request)

	if response.Code != stdhttp.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("expected 200, got %d body=%s", response.Code, string(body))
	}

	t.Run("preserves request path", func(t *testing.T) {
		if captured.path != "/api/v1/users/123" {
			t.Fatalf("expected proxied path /api/v1/users/123, got %q", captured.path)
		}
	})

	t.Run("routes to upstream host", func(t *testing.T) {
		if captured.host != "users.example.internal" {
			t.Fatalf("expected upstream host users.example.internal, got %q", captured.host)
		}
	})

	t.Run("strips authorization header", func(t *testing.T) {
		if captured.authorization != "" {
			t.Fatalf("expected Authorization not to be forwarded, got %q", captured.authorization)
		}
	})

	t.Run("forwards correlation ID", func(t *testing.T) {
		if captured.correlationID != "corr-123" {
			t.Fatalf("expected X-Correlation-Id to be forwarded, got %q", captured.correlationID)
		}
	})

	t.Run("strips hop-by-hop headers", func(t *testing.T) {
		if captured.connection != "" {
			t.Fatalf("expected Connection not to be forwarded, got %q", captured.connection)
		}
		if captured.removeMe != "" {
			t.Fatalf("expected X-Remove-Me not to be forwarded, got %q", captured.removeMe)
		}
	})

	t.Run("rebuilds forwarded headers from the gateway view", func(t *testing.T) {
		if captured.forwardedFor != "127.0.0.1" {
			t.Fatalf("expected X-Forwarded-For to be rebuilt, got %q", captured.forwardedFor)
		}
		if captured.forwardedHost != "127.0.0.1:8080" {
			t.Fatalf("expected X-Forwarded-Host to be rebuilt, got %q", captured.forwardedHost)
		}
		if captured.forwardedProto != "http" {
			t.Fatalf("expected X-Forwarded-Proto to be rebuilt, got %q", captured.forwardedProto)
		}
		if captured.forwardedHeader != "" {
			t.Fatalf("expected Forwarded to be removed, got %q", captured.forwardedHeader)
		}
	})
}

// TestProtectedRouteReturns429WhenRateLimited verifies 429 responses include rate-limit metadata.
func TestProtectedRouteReturns429WhenRateLimited(t *testing.T) {
	server := newTestServer(t, stubPinger{})
	server.authoriser = gateway.NewAuthoriser(
		stubGatewayRepository{record: token.Record{
			APIKey:        token.HashAPIKey("users-static-token", "secret"),
			RateLimit:     1,
			ExpiresAt:     time.Now().Add(time.Hour),
			AllowedRoutes: []string{"/api/v1/users/*"},
		}},
		stubGatewayLimiter{decision: ratelimit.Decision{Allowed: false, Limit: 1, Current: 2, RetryAfter: time.Second}},
		time.Now,
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodPost, "/api/v1/users/123", nil)
	request.Header.Set("Authorization", "Bearer users-static-token")
	server.Router().ServeHTTP(response, request)

	if response.Code != stdhttp.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", response.Code)
	}

	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("expected problem content type, got %q", got)
	}

	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("expected Retry-After header to be 1, got %q", got)
	}

	if got := response.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("expected X-RateLimit-Limit header to be 1, got %q", got)
	}

	if got := response.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Fatalf("expected X-RateLimit-Remaining header to be 0, got %q", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode rate limit response: %v", err)
	}

	if payload["type"] != "/problems/rate-limited" {
		t.Fatalf("expected type /problems/rate-limited, got %#v", payload["type"])
	}

	if payload["title"] != "Too Many Requests" {
		t.Fatalf("expected title Too Many Requests, got %#v", payload["title"])
	}

	if int(payload["status"].(float64)) != stdhttp.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %#v", payload["status"])
	}

	if payload["detail"] != "rate limit exceeded" {
		t.Fatalf("expected detail rate limit exceeded, got %#v", payload["detail"])
	}

	if payload["instance"] != "/api/v1/users/123" {
		t.Fatalf("expected instance path, got %#v", payload["instance"])
	}
}

// TestProxyReturns404WhenNoUpstreamRoute verifies unmatched resource paths return
// a problem response from the proxy layer rather than a gateway upstream error.
func TestProxyReturns404WhenNoUpstreamRoute(t *testing.T) {
	target, err := url.Parse("http://users.example.internal")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}

	proxy := NewProxy([]config.Route{{
		PathPrefix: "/api/v1/users",
		Target:     target,
	}}, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodPost, "/api/v1/users-archive/123", nil)
	proxy.Handler().ServeHTTP(response, request)

	if response.Code != stdhttp.StatusNotFound {
		t.Fatalf("expected 404, got %d", response.Code)
	}

	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode route-not-found response: %v", err)
	}

	if payload["type"] != "/problems/route-not-found" {
		t.Fatalf("expected type /problems/route-not-found, got %#v", payload["type"])
	}
}

type stubGatewayRepository struct {
	record token.Record
	err    error
}

// GetByHashedAPIKey returns the configured record for HTTP handler tests.
func (s stubGatewayRepository) GetByHashedAPIKey(context.Context, string) (token.Record, error) {
	return s.record, s.err
}

// Put satisfies the repository interface in tests that never persist data.
func (s stubGatewayRepository) Put(context.Context, string, token.Record) error {
	return nil
}

type stubGatewayLimiter struct {
	decision ratelimit.Decision
	err      error
}

// Allow returns the configured rate-limit decision for HTTP handler tests.
func (s stubGatewayLimiter) Allow(context.Context, string, int) (ratelimit.Decision, error) {
	return s.decision, s.err
}

type roundTripFunc func(*stdhttp.Request) (*stdhttp.Response, error)

// RoundTrip allows tests to inject a function as an http.RoundTripper.
func (f roundTripFunc) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	return f(request)
}

// newTestServer builds a server with a default users route for HTTP tests.
func newTestServer(t *testing.T, pinger stubPinger) *Server {
	t.Helper()
	return newServerWithUpstream(t, "http://example.com", pinger, token.Record{
		APIKey:        token.HashAPIKey("users-static-token", "secret"),
		RateLimit:     5,
		ExpiresAt:     time.Now().Add(time.Hour),
		AllowedRoutes: []string{"/api/v1/users/*"},
	}, ratelimit.Decision{
		Allowed: true,
		Limit:   5,
		Current: 1,
	}, roundTripFunc(func(request *stdhttp.Request) (*stdhttp.Response, error) {
		return (&stdhttp.Transport{}).RoundTrip(request)
	}))
}

// newServerWithUpstream builds a server instance with caller-supplied upstream and auth behaviour.
func newServerWithUpstream(t *testing.T, upstreamURL string, pinger stubPinger, record token.Record, decision ratelimit.Decision, transport stdhttp.RoundTripper) *Server {
	t.Helper()
	target, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	cfg := config.Config{
		ListenAddress:      ":8080",
		LogLevel:           "info",
		RedisAddress:       "127.0.0.1:6379",
		RedisTimeout:       time.Second,
		ProxyTimeout:       time.Second,
		RateLimitWindow:    time.Minute,
		TokenHashSecret:    "secret",
		TokenKeyPrefix:     "token",
		RateLimitKeyPrefix: "ratelimit",
		Routes: []config.Route{
			{PathPrefix: "/api/v1/users", Target: target},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, registry)
	if err != nil {
		t.Fatalf("build metrics: %v", err)
	}
	authoriser := gateway.NewAuthoriser(
		stubGatewayRepository{record: record},
		stubGatewayLimiter{decision: decision},
		time.Now,
	)

	proxy := NewProxy(cfg.Routes, time.Second, logger)
	proxy.transport = transport

	return NewServer(cfg, logger, metrics, pinger, authoriser, proxy)
}
