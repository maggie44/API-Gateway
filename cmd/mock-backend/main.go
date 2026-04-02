// Command mock-backend starts a simple upstream service used for local testing.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
)

type response struct {
	Service string              `json:"service"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
}

// main delegates to run so any deferred cleanup would execute before exit.
func main() {
	if err := run(); err != nil {
		slog.Error("mock backend exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run starts a tiny mock upstream service used for local gateway testing.
func run() error {
	serviceName := getenv("SERVICE_NAME", "mock-backend")
	listenAddress := getenv("LISTEN_ADDRESS", ":8081")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{}))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.Info("received request", slog.String("method", r.Method), slog.String("path", r.URL.Path))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response{
			Service: serviceName,
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: r.Header,
		})
	})

	logger.Info("starting mock backend", slog.String("service", serviceName), slog.String("addr", listenAddress))
	if err := http.ListenAndServe(listenAddress, mux); err != nil {
		return err
	}

	return nil
}

// getenv returns an environment variable when present, otherwise the fallback.
func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
