package session

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("session: not found")

type Session struct {
	IDHash    []byte
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UserAgent string
	IP        string
}

type Store interface {
	Create(ctx context.Context, s Session) error
	Get(ctx context.Context, idHash []byte) (Session, error)
	Touch(ctx context.Context, idHash []byte, expiresAt time.Time) error
	Delete(ctx context.Context, idHash []byte) error
	DeleteForUser(ctx context.Context, userID int64) error
}
