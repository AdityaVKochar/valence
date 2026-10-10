package users

import (
	"context"

	"connectrpc.com/connect"

	"github.com/AdityaVKochar/valence/gen/go/db"
	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/v1/valencev1connect"
	"github.com/AdityaVKochar/valence/services/api/internal/auth"
)

type Service struct {
	auth *auth.Service
}

var (
	_ valencev1connect.UserServiceHandler = (*Service)(nil)
	_ valencev1connect.AuthServiceHandler = (*Service)(nil)
)

func New(a *auth.Service) *Service { return &Service{auth: a} }

func (s *Service) GetMe(ctx context.Context, _ *connect.Request[valencev1.GetMeRequest]) (*connect.Response[valencev1.GetMeResponse], error) {
	u, err := auth.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&valencev1.GetMeResponse{User: ToProto(u)}), nil
}

func (s *Service) ListAuthProviders(context.Context, *connect.Request[valencev1.ListAuthProvidersRequest]) (*connect.Response[valencev1.ListAuthProvidersResponse], error) {
	resp := &valencev1.ListAuthProvidersResponse{LogoutUrl: "/auth/logout"}
	for _, p := range s.auth.Providers() {
		resp.Providers = append(resp.Providers, &valencev1.AuthProvider{Id: p.ID, DisplayName: p.DisplayName, LoginUrl: p.LoginURL})
	}
	return connect.NewResponse(resp), nil
}

func ToProto(u *db.User) *valencev1.User {
	out := &valencev1.User{Id: u.ID, Handle: u.Handle, DisplayName: u.DisplayName, Role: RoleToProto(u.Role)}
	if u.AvatarUrl != nil {
		out.AvatarUrl = *u.AvatarUrl
	}
	return out
}

func RoleToProto(r db.UserRole) valencev1.Role {
	switch r {
	case db.UserRoleContestant:
		return valencev1.Role_ROLE_CONTESTANT
	case db.UserRoleSetter:
		return valencev1.Role_ROLE_SETTER
	case db.UserRoleAdmin:
		return valencev1.Role_ROLE_ADMIN
	}
	return valencev1.Role_ROLE_UNSPECIFIED
}
