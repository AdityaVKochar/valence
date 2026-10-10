package metrics

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/v1/valencev1connect"
)

type pinger struct{ fail bool }

func (p pinger) Ping(context.Context, *connect.Request[valencev1.PingRequest]) (*connect.Response[valencev1.PingResponse], error) {
	if p.fail {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("down"))
	}
	return connect.NewResponse(&valencev1.PingResponse{}), nil
}

func TestRPCMetrics(t *testing.T) {
	reg := NewRegistry()
	rpc := NewRPC(reg)
	for _, fail := range []bool{false, true} {
		_, h := valencev1connect.NewHealthServiceHandler(pinger{fail}, connect.WithInterceptors(rpc.Interceptor()))
		srv := httptest.NewServer(h)
		c := valencev1connect.NewHealthServiceClient(srv.Client(), srv.URL)
		_, _ = c.Ping(context.Background(), connect.NewRequest(&valencev1.PingRequest{}))
		srv.Close()
	}
	rec := httptest.NewRecorder()
	Handler(reg).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`valence_rpc_requests_total{code="ok",procedure="/valence.v1.HealthService/Ping"} 1`,
		`valence_rpc_requests_total{code="unavailable",procedure="/valence.v1.HealthService/Ping"} 1`,
		`valence_rpc_duration_seconds_count{procedure="/valence.v1.HealthService/Ping"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}
