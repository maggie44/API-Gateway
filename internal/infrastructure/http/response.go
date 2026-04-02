package http

import (
	"encoding/json"
	"log/slog"
	stdhttp "net/http"

	"github.com/go-chi/chi/v5/middleware"

	httpgenerated "github.com/maggie44/api-gateway/internal/infrastructure/http/generated"
)

// writeJSON writes a JSON response body with the given HTTP status code.
func writeJSON(w stdhttp.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("write json response", slog.Any("error", err))
	}
}

// writeError writes an RFC 7807 problem-details response for gateway failures.
func writeError(w stdhttp.ResponseWriter, r *stdhttp.Request, statusCode int, problemType string, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	if requestID := middleware.GetReqID(r.Context()); requestID != "" {
		w.Header().Set("X-Request-Id", requestID)
	}
	w.WriteHeader(statusCode)
	instance := r.URL.Path
	// The generated ProblemDetails model keeps the runtime error shape aligned
	// with the RFC 7807 contract declared in the OpenAPI specification.
	if err := json.NewEncoder(w).Encode(httpgenerated.ProblemDetails{
		Type:     problemType,
		Title:    stdhttp.StatusText(statusCode),
		Status:   int32(statusCode),
		Detail:   detail,
		Instance: &instance,
	}); err != nil {
		slog.Error("write error response", slog.Any("error", err))
	}
}
