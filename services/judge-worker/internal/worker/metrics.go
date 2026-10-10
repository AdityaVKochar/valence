package worker

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	busy        prometheus.Gauge
	verdicts    *prometheus.CounterVec
	infraErrors prometheus.Counter
	duration    prometheus.Histogram
}

// NewMetrics registers the worker metrics on reg. A nil reg keeps them unregistered.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		busy: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valence_worker_busy_slots",
			Help: "Slots currently judging a submission.",
		}),
		verdicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "valence_worker_judged_total",
			Help: "Submissions judged, by verdict.",
		}, []string{"verdict"}),
		infraErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "valence_worker_infra_errors_total",
			Help: "Jobs returned to the queue after an infrastructure error.",
		}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "valence_worker_judge_seconds",
			Help:    "Wall time from lease to result.",
			Buckets: []float64{0.25, 0.5, 1, 2, 4, 8, 16, 32, 64},
		}),
	}
	if reg != nil {
		reg.MustRegister(m.busy, m.verdicts, m.infraErrors, m.duration)
	}
	return m
}
