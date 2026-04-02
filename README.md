# API Gateway Service

This repository contains a Go 1.25 API gateway. It proxies resource-based API requests, validates static bearer tokens stored in Redis as secure hashes, applies per-token rate limiting, exposes health and readiness endpoints outside authentication, and defines its HTTP contract from an OpenAPI specification.

## Quick start

The fastest way to run the full demo locally is:

```bash
cp .env.example .env
make infra.up
make seed.tokens
make build
make run
```

Then, in a second terminal:

```bash
make demo
```

When finished:

```bash
make infra.down
```

The demo exercises the full request flow:

- public `/healthz`
- public `/readyz`
- successful proxying to resource-based backends
- missing or invalid token returning `401`
- disallowed route returning `403`
- per-token rate limiting returning `429`
- Prometheus metrics at `/metrics`

## Stack and implementation

This service is implemented with:

- Go 1.25
- `chi` for routing and middleware composition
- `httputil.ReverseProxy` for upstream proxying
- `slog` with `samber/slog-chi` for structured access logging
- OpenAPI with `oapi-codegen` as the HTTP contract source of truth
- `github.com/redis/go-redis/v9` for Redis-backed token storage and rate limiting
- Prometheus client libraries for `/metrics`

`chi` was selected as the router to streamline the implementation of middleware chains and request routing while maintaining proximity to Go's standard `net/http` package. This choice balances development velocity with the goal of demonstrating understanding of fundamental HTTP concepts in Go, avoiding the abstraction layers present in heavier frameworks.

`httputil.ReverseProxy` is used for upstream proxying because it is part of Go's standard library, correctly handles the complexities of HTTP proxying (header forwarding, connection management, and body streaming), and requires no external dependencies. This demonstrates that sophisticated gateway functionality can be built with standard library primitives rather than specialized proxy libraries.

The code follows a pragmatic domain-driven structure:

- `internal/domain` contains token and rate-limit business rules.
- `internal/application` contains the authorisation use case.
- `internal/infrastructure/http` contains the OpenAPI-backed HTTP server, proxying, middleware, and metrics exposure.
- `internal/infrastructure/tokenstore` contains the Redis-backed token repository.
- `internal/infrastructure/ratelimit` contains the Redis-backed rate limiter.
- `internal/infrastructure/config` contains environment loading and parsing.
- `internal/infrastructure/observability` contains slog logger construction.
- `development/infrastructure` contains the local Docker Compose setup and static token seed data.

At a high level, the request flow is:

1. `chi` routes the request to the generated OpenAPI handler.
2. Health, readiness, and metrics stay public and bypass token checks.
3. Protected resource requests extract the bearer token from the `Authorization` header.
4. The gateway hashes the token with `TOKEN_HASH_SECRET`.
5. Redis is queried for the token record by hashed key.
6. The token record is validated for hash match, expiry, and allowed route.
7. Redis is used again for per-token rate limiting.
8. Accepted requests are proxied to the configured upstream service.

## OpenAPI

The HTTP contract lives under `openapi/v1` and is the source of truth for the gateway's own operational endpoints: `/healthz`, `/readyz`, and `/metrics`.

The proxy surface at `POST /api/v1/*` is deliberately kept outside the OpenAPI spec. Because the gateway does not own the upstream response shapes and the set of proxied resources is determined at deploy time by the `ROUTES` configuration, modelling proxy paths in the spec would force a spec and code change for every new upstream resource. Instead, a single chi wildcard route handles all proxied traffic, so adding a new resource only requires a `ROUTES` config change.

`oapi-codegen` is used to generate type-safe Go server and client code directly from the OpenAPI specification. This approach ensures that the HTTP contract and implementation stay synchronized—when the spec changes, the generated types update automatically, preventing the common problem of documentation drifting from implementation. It also eliminates hand-written model types and request/response validation code, reducing boilerplate while maintaining type safety. It demonstrates that a well-designed API contract enables efficient, reliable code generation rather than manual HTTP wiring.

The OpenAPI files are:

- [api.yaml](/Users/arranmagee/Documents/GitHub.nosync/API%20Gateway/openapi/v1/api.yaml) defines the operational API surface
- [cfg.yaml](/Users/arranmagee/Documents/GitHub.nosync/API%20Gateway/openapi/v1/cfg.yaml) defines `oapi-codegen` configuration
- [generate.go](/Users/arranmagee/Documents/GitHub.nosync/API%20Gateway/openapi/v1/generate.go) contains generation directives

The generated `gatewayapi` server and model types are used directly by the handwritten HTTP layer in:

- `internal/infrastructure/http/generated`
- `internal/clients/http/apigateway/generated`

Generate code with:

```bash
make generate
```

Error responses use RFC 7807 Problem Details with `application/problem+json`.

## Token model

The token structure is preserved in Redis as:

```json
{
  "api_key": "xxx-xxx-xxx",
  "rate_limit": 100,
  "expires_at": "2024-12-31T23:59:59Z",
  "allowed_routes": ["/api/v1/users/*", "/api/v1/products/*"]
}
```

### How tokens are created and provided

This project deliberately uses static tokens for local development and demonstration.

- The raw demo tokens live in `development/infrastructure/static-tokens.json`.
- The `make seed.tokens` command reads that file.
- Each raw token is hashed with HMAC-SHA256 using `TOKEN_HASH_SECRET`.
- The seed command writes the record to Redis under `token:<hash>`.
- Clients present the raw token as `Authorization: Bearer <token>`.
- The gateway hashes the presented token and looks up the corresponding Redis record.

This keeps the demo and review flow simple while still showing how a real gateway would avoid storing raw API keys in Redis.

### How tokens are stored

To avoid storing raw API keys in Redis, the gateway computes an HMAC-SHA256 hash of the presented bearer token using `TOKEN_HASH_SECRET`.

- The Redis key is `token:<hash>`.
- The `api_key` field stored in Redis contains the same hash, not the raw token.
- The raw static tokens only exist in `development/infrastructure/static-tokens.json` so they can be seeded for local development.

Static tokens are used intentionally to keep provisioning simple and make the validation flow easy to review.

## Rate limiting

Rate limiting is Redis-backed and works per token. Because the counters live in Redis rather than in process memory, multiple gateway replicas can share the same limit state. That means this implementation supports distributed rate limiting across instances, subject to all replicas using the same Redis and having reasonably aligned clocks.

This gateway uses a fixed-window limiter.

How it works:

- each token gets one Redis counter for the active time window
- all requests in the same window increment that counter
- when the counter exceeds the token's `rate_limit`, the gateway returns `429`
- the counter key expires automatically when the window ends

Pros:

- simple to explain and operate
- cheap in Redis because it only needs one counter per active token/window
- straightforward to share across multiple gateway replicas

Cons:

- less precise than sliding-window approaches
- allows burstiness at window boundaries
- consistency across replicas depends on clocks being reasonably aligned

This trade-off keeps the rate limiter easy to reason about while still demonstrating cross-instance coordination clearly.

## Error responses

Gateway errors follow RFC 7807 Problem Details. A typical error response looks like:

```json
{
  "type": "/problems/rate-limited",
  "title": "Too Many Requests",
  "status": 429,
  "detail": "rate limit exceeded",
  "instance": "/api/v1/users/123"
}
```

## Local development

Copy the example environment if you want to start from a clean template:

```bash
cp .env.example .env
```

The repository already includes a usable `.env` for local development, and both the gateway and the token seeding command load `.env` automatically.
For local development, `.env` keeps setup straightforward. In a production deployment, secrets such as `TOKEN_HASH_SECRET` and Redis credentials would typically come from a secrets manager such as AWS Secrets Manager instead of being sourced from a local env file.

Start Redis and the mock upstream services:

```bash
make infra.up
```

Seed the static tokens into Redis:

```bash
make seed.tokens
```

Build the gateway binary:

```bash
make build
```

Run the gateway locally:

```bash
make run
```

The local run flow stays deliberately simple and explicit.
A future local-developer convenience would be to add [`air`](https://github.com/air-verse/air) for hot reload during iterative changes, but it is intentionally not required for the current setup.

Run an end-to-end demo once the infrastructure is up, tokens are seeded, and the gateway is running:

```bash
make demo
```

Stop local infrastructure:

```bash
make infra.down
```

## Configuration

Required environment variables:

- `TOKEN_HASH_SECRET`
- `ROUTES`, example:
  `/api/v1/users=http://127.0.0.1:8081,/api/v1/products=http://127.0.0.1:8082`

Optional environment variables:

- `LISTEN_ADDRESS` default `:8080`
- `LOG_LEVEL` default `info`
- `REDIS_ADDRESS` default `127.0.0.1:6379`
- `REDIS_PASSWORD`
- `REDIS_DB` default `0`
- `REDIS_TIMEOUT` default `3s`
- `PROXY_TIMEOUT` default `15s`
- `RATE_LIMIT_WINDOW` default `1m`
- `TOKEN_KEY_PREFIX` default `token`
- `RATE_LIMIT_KEY_PREFIX` default `ratelimit`
- `STATIC_TOKEN_FILE` default `development/infrastructure/static-tokens.json`

Docker Compose environment variables (used only for local container orchestration):

- `REDIS_PORT` default `6379`
- `USERS_BACKEND_PORT` default `8081`
- `PRODUCTS_BACKEND_PORT` default `8082`

## Example requests

Health and readiness are intentionally not protected by auth:

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

Proxy a users request:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/users/123 \
  -H "Authorization: Bearer users-static-token"
```

Proxy a products request:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/products/sku-123 \
  -H "Authorization: Bearer products-static-token"
```

## Testing

Run formatting lint:

```bash
make lint
```

Run unit tests:

```bash
make test
```

Run the end-to-end local demo flow:

```bash
make demo
```

Run benchmarks:

```bash
make benchmark
```

Regenerate OpenAPI code:

```bash
make generate
```

## Container image

The Docker build uses:

- `golang:1.25-bookworm` for the build stage
- `gcr.io/distroless/base-debian13:nonroot` for the final runtime stages

The final runtime image is distroless for a smaller attack surface and a closer-to-production container shape.
The current Dockerfile uses one shared builder stage that compiles the local binaries needed by this repository, which keeps the setup simpler at the cost of some extra build time. In a production pipeline, these artefacts would usually be split into separate build flows or target-specific builder stages so each image only compiles the binary it actually ships.

## Design notes

- A circuit breaker is not implemented in the current build, but the proxy layer has an explicit extension point for adding one per upstream in future. That would help short-circuit repeated failures, reduce cascading load on unhealthy dependencies, and improve resilience during upstream incidents.
- Idempotency is not implemented for proxied write requests. That means a client retry after a timeout can still replay a `POST` against the upstream service. This is called out explicitly rather than partially implemented; a production gateway would likely combine an idempotency key policy with short-lived request-result storage for selected operations.
- Health and readiness stay public because operational checks should not depend on client credentials.
- Resource-based routes are used throughout the mock APIs and the gateway route examples.
- The OpenAPI spec is the source of truth for the public HTTP surface, and the server uses generated request/response models directly.

## Assumptions

- API keys, bearer tokens, and access tokens are treated here as opaque bearer credentials whose metadata is stored and validated via Redis rather than JWTs.
- The provided token structure is treated as server-side state, implying a stateful validation model based on Redis lookup instead of decoding claims from token contents.
- `expires_at` is interpreted as the token validity boundary and enforced in application logic; use of Redis TTL for token lifecycle enforcement is not explicitly required.
- Per-token rate limiting is required, but no specific algorithm or windowing strategy is mandated; a fixed-window style approach is therefore assumed.
- Distributed rate limiting is described as optional; however, storing counters in Redis already enables cross-instance coordination.
- Path-based routing is required, but matching semantics and route-configuration strategy are not fully prescribed; prefix-based route mapping is assumed.
- Token issuance and provisioning workflows are outside the scope of this repository and would typically be implemented or documented separately.
- Redis is treated as the runtime source of truth for token and rate-limit state, without an explicit separation between persistent storage and cache responsibilities.
