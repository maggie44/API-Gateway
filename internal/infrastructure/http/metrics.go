package http

import (
	stdhttp "net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics groups the Prometheus collectors and handler used by the HTTP layer.
type Metrics struct {
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	handler             stdhttp.Handler
}

// NewMetrics registers the gateway collectors against the provided registry and
// returns the reusable metrics handler/middleware dependencies.
func NewMetrics(registerer prometheus.Registerer, gatherer prometheus.Gatherer) (*Metrics, error) {
	httpRequestsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_http_requests_total",
		Help: "Total HTTP requests handled by the gateway.",
	}, []string{"method", "route", "status"})

	httpRequestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})

	collectorsToRegister := []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		httpRequestsTotal,
		httpRequestDuration,
	}

	for _, collector := range collectorsToRegister {
		if err := registerer.Register(collector); err != nil {
			return nil, err
		}
	}

	return &Metrics{
		httpRequestsTotal:   httpRequestsTotal,
		httpRequestDuration: httpRequestDuration,
		handler:             promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}),
	}, nil
}

// Handler exposes the Prometheus registry on the standard /metrics endpoint.
func (m *Metrics) Handler() stdhttp.Handler {
	return m.handler
}
