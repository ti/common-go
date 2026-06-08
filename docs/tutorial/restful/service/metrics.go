package service

// Global Monitoring Demo Monitoring

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// demoCounter is a test counter for demonstration purposes.
var _ = promauto.NewCounter(prometheus.CounterOpts{
	Namespace: "cms",
	Subsystem: "demo",
	Name:      "total",
	Help:      "the demo counter",
})
