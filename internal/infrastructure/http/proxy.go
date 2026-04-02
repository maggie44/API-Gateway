package http

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	stdhttp "net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/maggie44/api-gateway/internal/infrastructure/config"
)

// Proxy holds the upstream routing table and outbound transport used for proxying.
type Proxy struct {
	routes    []config.Route
	timeout   time.Duration
	logger    *slog.Logger
	transport stdhttp.RoundTripper
}

// NewProxy creates the reverse-proxy adapter used by the gateway handlers.
func NewProxy(routes []config.Route, timeout time.Duration, logger *slog.Logger) *Proxy {
	return &Proxy{
		routes:  routes,
		timeout: timeout,
		logger:  logger,
		// The transport is created once and shared across requests so connection
		// pooling, keep-alives, and HTTP/2 sessions can be reused efficiently.
		transport: defaultTransport(),
	}
}

// Match resolves the upstream target for an incoming request path.
func (p *Proxy) Match(path string) (*url.URL, error) {
	requestPath := canonicalPath(path)

	for _, route := range p.routes {
		if requestPath == route.PathPrefix || strings.HasPrefix(requestPath, route.PathPrefix+"/") {
			return route.Target, nil
		}
	}

	return nil, fmt.Errorf("no upstream configured for path %q", requestPath)
}

// Handler returns the HTTP handler that forwards requests to the matched upstream.
func (p *Proxy) Handler() stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		requestPath := canonicalPath(r.URL.Path)
		target, err := p.Match(requestPath)
		if err != nil {
			writeError(w, r, stdhttp.StatusNotFound, "/problems/route-not-found", err.Error())
			return
		}

		// - A circuit breaker could be introduced around per-upstream proxy execution
		// here in future so repeated upstream failures temporarily short-circuit new
		// requests and protect both this gateway and the dependency from cascading load.
		// - A future optimisation could prebuild one ReverseProxy per configured route
		// and reuse it here. The expected gain is likely modest because the shared
		// transport already preserves the important connection pooling and HTTP/2
		// reuse behaviour, so this is left in the simpler per-request form for now.
		proxy := &httputil.ReverseProxy{
			Rewrite: func(proxyRequest *httputil.ProxyRequest) {
				// SetURL copies the target scheme/host and joins any base path on the
				// target with the incoming request path.
				proxyRequest.SetURL(target)
				// Keep the outbound Host aligned with the upstream target rather than the
				// original gateway host header.
				proxyRequest.Out.Host = target.Host
				copyForwardHeaders(proxyRequest.Out, proxyRequest.In)
			},
			// Reusing the shared transport is what preserves pooled outbound
			// connections and HTTP/2 reuse even though the proxy wrapper itself is
			// currently constructed per request.
			Transport: p.transport,
			ErrorHandler: func(writer stdhttp.ResponseWriter, request *stdhttp.Request, proxyErr error) {
				p.logger.Error("proxy request failed",
					slog.String("path", request.URL.Path),
					slog.String("target", target.String()),
					slog.String("error", proxyErr.Error()),
				)
				writeError(writer, request, stdhttp.StatusBadGateway, "/problems/proxy-error", "upstream request failed")
			},
		}

		ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
		defer cancel()
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

// defaultTransport builds the outbound HTTP transport with conservative production defaults.
func defaultTransport() stdhttp.RoundTripper {
	return &stdhttp.Transport{
		// Uses proxy settings from the environment variables HTTP_PROXY, HTTPS_PROXY, NO_PROXY.
		Proxy: stdhttp.ProxyFromEnvironment,

		// Dialer with TCP timeout and keep-alive settings.
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,

		// Maximum number of idle (keep-alive) connections across all hosts.
		MaxIdleConns: 400,

		// Maximum number of idle connections per host for reuse.
		MaxIdleConnsPerHost: 40,

		// Maximum number of connections per host, including active and idle connections.
		MaxConnsPerHost: 80,

		// Total idle timeout per connection before it is closed.
		IdleConnTimeout: 90 * time.Second,

		// Maximum time to wait for TLS handshake to complete.
		TLSHandshakeTimeout: 5 * time.Second,

		// Maximum time to wait for the server's first response when expecting 100-continue.
		ExpectContinueTimeout: 1 * time.Second,

		// Maximum time to wait for response headers after sending the request.
		ResponseHeaderTimeout: 5 * time.Second,

		// Compression remains on the standard-library default path for now; explicit
		// request/response compression policy could be configured here later if needed.
	}
}

// copyForwardHeaders clones end-to-end headers and adds standard forwarded metadata.
func copyForwardHeaders(outbound, inbound *stdhttp.Request) {
	outbound.Header = inbound.Header.Clone()
	for _, header := range hopByHopHeaders {
		outbound.Header.Del(header)
	}
	removeConnectionHeaders(outbound.Header, inbound.Header)
	// Forwarding headers are rebuilt from the current gateway view of the request
	// rather than trusting client-supplied values from the public edge.
	outbound.Header.Del("Forwarded")
	outbound.Header.Del("X-Forwarded-For")
	outbound.Header.Del("X-Forwarded-Host")
	outbound.Header.Del("X-Forwarded-Proto")
	outbound.Header.Del("X-Real-Ip")
	// The gateway consumes the bearer credential itself, so it must not forward that
	// API key to the upstream service.
	outbound.Header.Del("Authorization")

	setForwardHeader(outbound.Header, "X-Forwarded-For", clientIP(inbound.RemoteAddr))
	setForwardHeader(outbound.Header, "X-Forwarded-Host", inbound.Host)
	setForwardHeader(outbound.Header, "X-Forwarded-Proto", schemeForRequest(inbound))
}

// removeConnectionHeaders strips any additional hop-by-hop headers nominated by Connection.
func removeConnectionHeaders(outbound, inbound stdhttp.Header) {
	for _, connectionValue := range inbound.Values("Connection") {
		for token := range strings.SplitSeq(connectionValue, ",") {
			// RFC hop-by-hop handling allows Connection to nominate additional
			// headers that must not be forwarded beyond this proxy hop. For example,
			// if a client sends `Connection: keep-alive, X-Remove-Me`, the made-up
			// `X-Remove-Me` header becomes hop-by-hop for that request and should be
			// stripped here. The special behaviour comes from Connection naming the
			// header, not from `X-Remove-Me` itself being an RFC-defined header.
			headerName := stdhttp.CanonicalHeaderKey(strings.TrimSpace(token))
			if headerName == "" {
				continue
			}
			outbound.Del(headerName)
		}
	}
}

// setForwardHeader adds a forwarded header only when a value is available.
func setForwardHeader(header stdhttp.Header, key, value string) {
	if value == "" {
		return
	}
	header.Set(key, value)
}

// clientIP extracts the host portion of the remote address for X-Forwarded-For.
func clientIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return remoteAddress
	}
	return host
}

// schemeForRequest reports the inbound request scheme for forwarding headers.
func schemeForRequest(request *stdhttp.Request) string {
	if request.TLS != nil {
		return "https"
	}
	return "http"
}

var hopByHopHeaders = []string{
	// Connection carries per-hop options and can also nominate additional
	// headers that must be stripped before forwarding.
	"Connection",
	// Keep-Alive controls connection persistence for the current TCP hop only.
	"Keep-Alive",
	// Proxy-Authenticate is used by an intermediary to challenge the direct client.
	"Proxy-Authenticate",
	// Proxy-Authorization carries credentials intended only for the next proxy hop.
	"Proxy-Authorization",
	// TE advertises accepted transfer codings for this hop, apart from trailers.
	"Te",
	// Trailer announces which trailer fields may appear after the message body.
	"Trailer",
	// Transfer-Encoding describes message framing for this hop and must not leak upstream.
	"Transfer-Encoding",
	// Upgrade requests a protocol switch, for example HTTP to WebSocket, on this hop only.
	"Upgrade",
}
