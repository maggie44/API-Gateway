// Package http contains the HTTP server, middleware, and reverse-proxy adapters for the gateway.
package http

import (
	stdhttp "net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	slogchi "github.com/samber/slog-chi"
)

// accessLogMiddleware uses slog-chi so the gateway keeps chi-compatible request
// logging while emitting structured slog entries.
func (s *Server) accessLogMiddleware(next stdhttp.Handler) stdhttp.Handler {
	return slogchi.NewWithConfig(s.logger, slogchi.Config{
		WithRequestID: true,
		Filters: []slogchi.Filter{
			// Health and readiness are typically scraped frequently by platforms
			// and load balancers, and metrics is scraped frequently by Prometheus,
			// so suppress their access logs to avoid noisy operational output.
			slogchi.IgnorePath("/healthz", "/readyz", "/metrics"),
		},
	})(next)
}

// metricsMiddleware records request counters and latency histograms for Prometheus.
func (s *Server) metricsMiddleware(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		// Skip recording the scrape endpoint in the generic request metrics so
		// observability traffic does not distort application request volumes.
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		startedAt := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		routePattern := chi.RouteContext(r.Context()).RoutePattern()
		if routePattern == "" {
			routePattern = "unknown"
		}
		s.metrics.httpRequestsTotal.WithLabelValues(r.Method, routePattern, statusLabel(wrapped.Status())).Inc()
		s.metrics.httpRequestDuration.WithLabelValues(r.Method, routePattern).Observe(time.Since(startedAt).Seconds())
	})
}

// statusLabel normalises the response status into a Prometheus-safe label value.
func statusLabel(status int) string {
	if status == 0 {
		return "200"
	}
	return strconv.Itoa(status)
}
