package service

// Global Monitoring Demo Monitoring

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// demoCounter test counter
var demoCounter = promauto.NewCounter(prometheus.CounterOpts{
	Namespace: "cms",
	Subsystem: "demo",
	Name:      "total",
	Help:      "the demo counter",
})
