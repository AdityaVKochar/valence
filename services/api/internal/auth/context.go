package auth

import (
	"context"
	"errors"
	"slices"

	"connectrpc.com/connect"

	"github.com/AdityaVKochar/valence/gen/go/db"
)

type ctxKey struct{}

func WithUser(ctx context.Context, u *db.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func UserFrom(ctx context.Context) (*db.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*db.User)
	return u, ok && u != nil
}

func RequireUser(ctx context.Context) (*db.User, error) {
	u, ok := UserFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("log in to do this"))
	}
	return u, nil
}

func RequireRole(ctx context.Context, roles ...db.UserRole) (*db.User, error) {
	u, err := RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(roles, u.Role) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("your role cannot do this"))
	}
	return u, nil
}

func IsStaff(u *db.User) bool {
	return u != nil && (u.Role == db.UserRoleSetter || u.Role == db.UserRoleAdmin)
}

func IsAdmin(u *db.User) bool {
	return u != nil && u.Role == db.UserRoleAdmin
}
