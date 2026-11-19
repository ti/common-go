package background

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type metricSet struct {
	taskRuns     *prometheus.CounterVec
	taskDuration *prometheus.HistogramVec
	tasksRunning *prometheus.GaugeVec
	panics       *prometheus.CounterVec

	jobsSubmitted *prometheus.CounterVec
	jobsFinished  *prometheus.CounterVec
	jobRetries    *prometheus.CounterVec
	jobDuration   *prometheus.HistogramVec
	jobsQueued    *prometheus.GaugeVec
	jobsRunning   *prometheus.GaugeVec
}

// durationBuckets cover background work from milliseconds to an hour.
var durationBuckets = []float64{0.01, 0.1, 0.5, 1, 5, 15, 60, 300, 900, 3600}

// metrics are process-wide and registered once on the default registry, like
// the grpcmux collectors. Task names, job kinds and queue names are label
// values: keep them static and few.
var metrics = sync.OnceValue(func() *metricSet {
	f := promauto.With(prometheus.DefaultRegisterer)
	return &metricSet{
		taskRuns: f.NewCounterVec(prometheus.CounterOpts{
			Name: "background_task_runs_total",
			Help: "Runs of supervised goroutines and periodic tasks by result (ok, error, panic, skipped).",
		}, []string{"name", "result"}),
		taskDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "background_task_duration_seconds",
			Help:    "Duration of supervised goroutines and periodic task runs.",
			Buckets: durationBuckets,
		}, []string{"name"}),
		tasksRunning: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "background_tasks_running",
			Help: "Supervised goroutines and periodic task runs in progress.",
		}, []string{"name"}),
		panics: f.NewCounterVec(prometheus.CounterOpts{
			Name: "background_panics_recovered_total",
			Help: "Panics recovered in background tasks and job handlers.",
		}, []string{"name"}),
		jobsSubmitted: f.NewCounterVec(prometheus.CounterOpts{
			Name: "background_jobs_submitted_total",
			Help: "Job submissions by result (accepted, duplicate, rejected_full, rejected_shutdown, error).",
		}, []string{"queue", "kind", "result"}),
		jobsFinished: f.NewCounterVec(prometheus.CounterOpts{
			Name: "background_jobs_finished_total",
			Help: "Jobs that reached a final state (succeeded, failed, cancelled, interrupted).",
		}, []string{"queue", "kind", "state"}),
		jobRetries: f.NewCounterVec(prometheus.CounterOpts{
			Name: "background_job_retries_total",
			Help: "Job attempts scheduled again after a retryable error.",
		}, []string{"queue", "kind"}),
		jobDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "background_job_duration_seconds",
			Help:    "Duration of one job attempt.",
			Buckets: durationBuckets,
		}, []string{"queue", "kind"}),
		jobsQueued: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "background_jobs_queued",
			Help: "Jobs waiting for a worker (including jobs waiting for a retry).",
		}, []string{"queue"}),
		jobsRunning: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "background_jobs_running",
			Help: "Jobs being executed.",
		}, []string{"queue"}),
	}
})
