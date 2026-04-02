// Command gateway starts the API gateway service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/maggie44/api-gateway/internal/application/gateway"
	"github.com/maggie44/api-gateway/internal/infrastructure/config"
	infrahttp "github.com/maggie44/api-gateway/internal/infrastructure/http"
	"github.com/maggie44/api-gateway/internal/infrastructure/observability"
	infraratelimit "github.com/maggie44/api-gateway/internal/infrastructure/ratelimit"
	"github.com/maggie44/api-gateway/internal/infrastructure/tokenstore"
)

// main delegates to run so deferred cleanup can complete before the process exits.
func main() {
	if err := run(); err != nil {
		slog.Error("service exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run wires the gateway dependencies, starts the HTTP server, and handles
// graceful shutdown when the process receives a termination signal.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := observability.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddress,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		DialTimeout:  cfg.RedisTimeout,
		ReadTimeout:  cfg.RedisTimeout,
		WriteTimeout: cfg.RedisTimeout,
	})

	tokenRepository := tokenstore.NewRedisRepository(redisClient, cfg.TokenKeyPrefix)
	rateLimiter := infraratelimit.NewRedisFixedWindowLimiter(redisClient, cfg.RateLimitWindow, cfg.RateLimitKeyPrefix)
	authoriser := gateway.NewAuthoriser(tokenRepository, rateLimiter, time.Now)
	proxy := infrahttp.NewProxy(cfg.Routes, cfg.ProxyTimeout, logger)
	metricsRegistry := prometheus.NewRegistry()
	metrics, err := infrahttp.NewMetrics(metricsRegistry, metricsRegistry)
	if err != nil {
		return fmt.Errorf("build metrics: %w", err)
	}
	server := infrahttp.NewServer(cfg, logger, metrics, infrahttp.NewRedisPinger(redisClient), authoriser, proxy)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           server.Router(),
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.ProxyTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Buffer a single startup/runtime failure so the serve goroutine can report it
	// even if the main goroutine is currently blocked waiting on a signal.
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gateway", slog.String("addr", cfg.ListenAddress))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrors <- fmt.Errorf("listen and serve: %w", err)
		}
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Either the server failed to start, or the process received a shutdown signal.
	select {
	case err := <-serverErrors:
		return err
	case <-signalContext.Done():
	}

	// Shutdown stops accepting new requests and waits for in-flight requests to finish
	// until the shutdown timeout is reached.
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownContext); err != nil {
		return err
	}

	if err := redisClient.Close(); err != nil {
		return fmt.Errorf("close redis client: %w", err)
	}

	// Drain any late server error after Shutdown so a real serve failure is not
	// silently ignored during process exit.
	select {
	case err := <-serverErrors:
		if err != nil {
			return err
		}
	default:
	}

	return nil
}
