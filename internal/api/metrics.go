package api

import (
	"github.com/prometheus/client_golang/prometheus"
)

type metrics struct {
	requests  *prometheus.CounterVec
	latency   *prometheus.HistogramVec
	redirects prometheus.Counter
	created   prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snaplink_http_requests_total",
			Help: "HTTP requests by route and status code.",
		}, []string{"route", "code"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "snaplink_http_request_duration_seconds",
			Help:    "HTTP request latency by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		redirects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "snaplink_redirects_total",
			Help: "Successful short link redirects.",
		}),
		created: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "snaplink_links_created_total",
			Help: "Short links created.",
		}),
	}
	reg.MustRegister(m.requests, m.latency, m.redirects, m.created)
	return m
}
