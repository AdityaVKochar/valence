package health

import (
	"context"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"

	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/v1/valencev1connect"
	"github.com/AdityaVKochar/valence/pkg/version"
)

type Service struct {
	pool *pgxpool.Pool
}

var _ valencev1connect.HealthServiceHandler = (*Service)(nil)

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Ping(ctx context.Context, _ *connect.Request[valencev1.PingRequest]) (*connect.Response[valencev1.PingResponse], error) {
	var dbVersion string
	if err := s.pool.QueryRow(ctx, "SELECT version()").Scan(&dbVersion); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&valencev1.PingResponse{ServerVersion: version.String(), DatabaseVersion: dbVersion}), nil
}
