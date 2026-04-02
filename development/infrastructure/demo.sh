#!/usr/bin/env sh

set -eu

# Load the local demo environment so the script uses the same ports and addresses
# as the rest of the repository tooling.
set -a
. ./.env
set +a

# Build the gateway base URL from the configured listen address.
base_url="http://127.0.0.1${LISTEN_ADDRESS}"

# Print a clearly separated heading for each part of the end-to-end demo flow.
section() {
	echo
	printf '\033[1;36m== %s ==\033[0m\n' "$1"
}

# Reuse the common curl flags used throughout the demo so the HTTP exchange is
# shown consistently, including response status and headers.
request() {
	curl -sS -i "$@"
}

# Demonstrate the unauthenticated liveness probe.
section "Health check"
request "${base_url}/healthz"

# Demonstrate the unauthenticated readiness probe, which also exercises Redis
# connectivity because readiness depends on Redis being available.
section "Readiness check"
request "${base_url}/readyz"

# Demonstrate a successful authenticated proxy request to the users backend.
section "Successful users proxy request"
request -X POST "${base_url}/api/v1/users/123" \
	-H "Authorization: Bearer users-static-token" \
	-H "X-Correlation-Id: demo-users-123" \
	-H "Content-Type: application/json" \
	-d '{"resource":"users","id":"123"}'

# Demonstrate a successful authenticated proxy request to the products backend.
section "Successful products proxy request"
request -X POST "${base_url}/api/v1/products/sku-123" \
	-H "Authorization: Bearer products-static-token" \
	-H "X-Correlation-Id: demo-products-sku-123" \
	-H "Content-Type: application/json" \
	-d '{"resource":"products","id":"sku-123"}'

# Demonstrate that protected routes reject requests with no bearer token.
section "Missing token returns 401"
request -X POST "${base_url}/api/v1/users/123"

# Demonstrate that an unknown bearer token is treated as invalid and returns 401.
section "Invalid token returns 401"
request -X POST "${base_url}/api/v1/users/123" \
	-H "Authorization: Bearer invalid-demo-token"

# Demonstrate route-level authorisation by presenting a valid users token to the
# products route, which should be rejected with 403.
section "Route policy returns 403"
request -X POST "${base_url}/api/v1/products/sku-123" \
	-H "Authorization: Bearer users-static-token"

# Demonstrate that path-based routing correctly directs requests to different
# microservices based on the URL path.
section "Path-based routing to different microservices"
echo "Routing /api/v1/users/* to users backend:"
request -X POST "${base_url}/api/v1/users/routing-demo" \
	-H "Authorization: Bearer users-static-token" \
	-H "Content-Type: application/json" \
	-d '{"resource":"users"}' | head -n 1
echo
echo "Routing /api/v1/products/* to products backend:"
request -X POST "${base_url}/api/v1/products/routing-demo" \
	-H "Authorization: Bearer products-static-token" \
	-H "Content-Type: application/json" \
	-d '{"resource":"products"}' | head -n 1

# Demonstrate fixed-window rate limiting by repeatedly calling the users route
# with a token seeded with a low request limit.
section "Rate limiting returns 429 after repeated requests"
attempt=1
while [ "${attempt}" -le 5 ]; do
	status="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${base_url}/api/v1/users/rate-limit-demo" \
		-H "Authorization: Bearer users-static-token")"
	printf 'users-static-token request %s -> HTTP %s\n' "${attempt}" "${status}"
	attempt=$((attempt + 1))
done

# Show the full 429 response after the burst so the retry headers and RFC 7807
# problem body can be inspected directly.
request -X POST "${base_url}/api/v1/users/rate-limit-demo" \
	-H "Authorization: Bearer users-static-token"

# Demonstrate that expired tokens are rejected with 401.
section "Expired token returns 401"
request -X POST "${base_url}/api/v1/users/expired-test" \
	-H "Authorization: Bearer expired-token"

# Demonstrate cross-service token rejection by attempting to use a users-only token
# on a products endpoint (returns 403 because the token is not allowed that route).
section "Token route policy enforcement"
request -X POST "${base_url}/api/v1/products/policy-test" \
	-H "Authorization: Bearer users-static-token"

# Demonstrate that custom headers are forwarded to the upstream service.
section "Custom header forwarding"
request -X POST "${base_url}/api/v1/users/headers-test" \
	-H "Authorization: Bearer users-static-token" \
	-H "X-Custom-Header: custom-value" \
	-H "X-Request-Id: demo-headers-123" \
	-H "Content-Type: application/json" \
	-d '{"test":"headers"}' | grep -E 'X-Custom-Header|X-Request-Id' || true

# Demonstrate rate limiting under concurrent load by launching parallel requests.
section "Concurrent requests and rate limiting"
# Start 7 concurrent requests to a fresh users token (limit is 5)
# Use a subshell to run them concurrently
(
	for i in 1 2 3 4 5 6 7; do
		(
			status="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${base_url}/api/v1/users/concurrent-demo" \
				-H "Authorization: Bearer users-static-token")"
			printf 'concurrent request %d -> HTTP %s\n' "$i" "$status"
		) &
	done
	wait
) | sort

# Show the Prometheus metrics endpoint so request counters and timings are visible
# after exercising the gateway flow above.
section "Metrics endpoint"
curl -sS "${base_url}/metrics" | grep -E 'http_requests_total|http_request_duration_seconds' | head -n 4 || true

# End with a final newline so the shell prompt is visually separated from output.
echo
