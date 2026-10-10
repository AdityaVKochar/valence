package metrics

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
}

type RPC struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewRPC(reg prometheus.Registerer) *RPC {
	m := &RPC{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "valence_rpc_requests_total",
			Help: "RPCs handled, by procedure and Connect status code.",
		}, []string{"procedure", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "valence_rpc_duration_seconds",
			Help:    "RPC handling time, by procedure.",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}, []string{"procedure"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

func (m *RPC) Interceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			code := "ok"
			if err != nil {
				code = connect.CodeOf(err).String()
				var ce *connect.Error
				if !errors.As(err, &ce) {
					code = connect.CodeUnknown.String()
				}
			}
			procedure := req.Spec().Procedure
			m.requests.WithLabelValues(procedure, code).Inc()
			m.duration.WithLabelValues(procedure).Observe(time.Since(start).Seconds())
			return resp, err
		}
	})
}
