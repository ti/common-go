package http

import prom "github.com/prometheus/client_golang/prometheus"

var defaultClientMetrics *clientMetrics

func init() {
	defaultClientMetrics = newClientMetrics()
	prom.MustRegister(defaultClientMetrics.clientHandledCounter)
	prom.MustRegister(defaultClientMetrics.clientHandledHistogram)
}

// clientMetrics represents a collection of metrics to be registered on a
// Prometheus metrics registry for a http client.
type clientMetrics struct {
	clientHandledCounter       *prom.CounterVec
	clientHandledHistogram     *prom.HistogramVec
	clientHandledHistogramOpts prom.HistogramOpts
}

// newClientMetrics returns a ClientMetrics object. Use a new instance of
// ClientMetrics when not using the default Prometheus metrics registry, for
// example when wanting to control, which metrics are added to a registry as
// opposed to automatically adding metrics via init functions.
func newClientMetrics() *clientMetrics {
	opts := prom.HistogramOpts{
		Name:    "http_client_handling_seconds",
		Help:    "Histogram of response latency (seconds) of the http until it is finished by the application.",
		Buckets: prom.DefBuckets,
	}
	return &clientMetrics{
		clientHandledCounter: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "http_client_handled_total",
				Help: "Total number of RPCs completed by the client, regardless of success or failure.",
			}, []string{"http_scheme", "http_service", "http_path", "http_status"}),
		clientHandledHistogramOpts: opts,
		clientHandledHistogram: prom.NewHistogramVec(
			opts,
			[]string{"http_scheme", "http_service", "http_path"},
		),
	}
}
