package http

import (
	"context"
	"errors"
	"log/slog"
	"math"
	stdhttp "net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"

	"github.com/maggie44/api-gateway/internal/application/gateway"
	"github.com/maggie44/api-gateway/internal/domain/ratelimit"
	"github.com/maggie44/api-gateway/internal/domain/token"
	"github.com/maggie44/api-gateway/internal/infrastructure/config"
	httpgenerated "github.com/maggie44/api-gateway/internal/infrastructure/http/generated"
)

type pinger interface {
	Ping(ctx context.Context) error
}

// RedisPinger adapts a go-redis client to the small error-oriented readiness
// contract used by the HTTP layer.
type RedisPinger struct {
	client *redis.Client
}

// NewRedisPinger creates the Redis readiness adapter used by the HTTP server.
func NewRedisPinger(client *redis.Client) *RedisPinger {
	return &RedisPinger{client: client}
}

// Ping reports Redis availability as a plain error so the HTTP layer does not
// need to know about go-redis command wrapper types.
func (p *RedisPinger) Ping(ctx context.Context) error {
	return p.client.Ping(ctx).Err()
}

// Server implements the generated OpenAPI server interface for the gateway endpoints.
type Server struct {
	config     config.Config
	logger     *slog.Logger
	metrics    *Metrics
	redis      pinger
	authoriser *gateway.Authoriser
	proxy      *Proxy
}

// NewServer builds the OpenAPI-backed HTTP server implementation.
func NewServer(
	cfg config.Config,
	logger *slog.Logger,
	metrics *Metrics,
	redis pinger,
	authoriser *gateway.Authoriser,
	proxy *Proxy,
) *Server {
	return &Server{
		config:     cfg,
		logger:     logger,
		metrics:    metrics,
		redis:      redis,
		authoriser: authoriser,
		proxy:      proxy,
	}
}

// Router constructs the chi router and attaches the generated OpenAPI handlers.
func (s *Server) Router() stdhttp.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(s.accessLogMiddleware)
	router.Use(s.metricsMiddleware)
	router.Use(middleware.Recoverer)
	// CORS middleware would be added here if this gateway needed to serve browser
	// clients directly rather than server-to-server API traffic.

	// Mount the generated OpenAPI handlers for operational endpoints (healthz,
	// readyz, metrics) which have well-defined response shapes.
	httpgenerated.HandlerFromMux(s, router)

	// The proxy catch-all lives outside the OpenAPI spec so new upstream
	// resources only require a ROUTES config change, not a spec or code change.
	router.Post("/api/v1/*", s.proxyProtectedResource)

	return router
}

// GetHealthz answers the unauthenticated liveness probe.
func (s *Server) GetHealthz(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	writeJSON(w, stdhttp.StatusOK, httpgenerated.StatusResponse{Status: "ok"})
}

// GetReadyz answers the unauthenticated readiness probe by checking Redis connectivity.
func (s *Server) GetReadyz(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.config.RedisTimeout)
	defer cancel()

	// Readiness is intentionally tied to Redis because protected traffic depends on
	// Redis for both token lookup and rate-limit state.
	if err := s.redis.Ping(ctx); err != nil {
		writeError(w, r, stdhttp.StatusServiceUnavailable, "/problems/redis-unavailable", "redis is unavailable")
		return
	}

	writeJSON(w, stdhttp.StatusOK, httpgenerated.StatusResponse{Status: "ready"})
}

// GetMetrics serves the Prometheus metrics endpoint.
func (s *Server) GetMetrics(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	s.metrics.Handler().ServeHTTP(w, r)
}

// proxyProtectedResource runs authorisation first and forwards only admitted requests.
func (s *Server) proxyProtectedResource(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	request := r.Clone(r.Context())
	request.URL.Path = canonicalPath(r.URL.Path)
	request.URL.RawPath = ""

	if !s.authoriseRequest(w, request) {
		return
	}

	// Idempotency-key handling could be layered here for selected write operations
	// if the gateway later needed to deduplicate retried client requests safely.
	s.proxy.Handler().ServeHTTP(w, request)
}

// authoriseRequest maps application authorisation failures to HTTP responses.
func (s *Server) authoriseRequest(w stdhttp.ResponseWriter, r *stdhttp.Request) bool {
	rawToken, err := extractBearerToken(r.Header.Get("Authorization"))
	if err != nil {
		statusCode := stdhttp.StatusUnauthorized
		problemType := "/problems/invalid-token"
		if errors.Is(err, errMissingBearerToken) {
			problemType = "/problems/missing-token"
		}
		writeError(w, r, statusCode, problemType, err.Error())
		return false
	}

	hashedToken := token.HashAPIKey(rawToken, s.config.TokenHashSecret)
	// The HTTP layer hashes the raw bearer token before calling the application
	// service so neither the application layer nor Redis lookups need to handle
	// the plain-text API key.
	result, err := s.authoriser.Authorise(r.Context(), hashedToken, r.URL.Path)
	if err != nil {
		switch {
		case errors.Is(err, token.ErrNotFound), errors.Is(err, token.ErrExpired), errors.Is(err, token.ErrHashMismatch):
			writeError(w, r, stdhttp.StatusUnauthorized, "/problems/invalid-token", err.Error())
		case errors.Is(err, token.ErrRouteNotAllowed):
			writeError(w, r, stdhttp.StatusForbidden, "/problems/route-forbidden", err.Error())
		case errors.Is(err, ratelimit.ErrLimitExceeded):
			// A small in-memory deny cache keyed by hashed token could be added here later so
			// repeated requests during an active 429 window only recheck Redis at most once per
			// second. That would reduce load during bursts without changing the source of truth.
			writeRateLimitHeaders(w, result.Decision)
			writeError(w, r, stdhttp.StatusTooManyRequests, "/problems/rate-limited", "rate limit exceeded")
		default:
			s.logger.Error("authorise request", slog.String("path", r.URL.Path), slog.String("error", err.Error()))
			writeError(w, r, stdhttp.StatusServiceUnavailable, "/problems/authorisation-unavailable", "authorisation dependency unavailable")
		}
		return false
	}

	return true
}

// writeRateLimitHeaders adds standard rate-limit metadata to a 429 response.
func writeRateLimitHeaders(w stdhttp.ResponseWriter, decision ratelimit.Decision) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(decision.Limit))

	remaining := max(decision.Limit-decision.Current, 0)
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))

	if decision.RetryAfter <= 0 {
		return
	}

	retryAfterSeconds := max(int(math.Ceil(decision.RetryAfter.Seconds())), 1)
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
}
