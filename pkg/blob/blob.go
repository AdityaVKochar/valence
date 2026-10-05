package blob

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrNotFound = errors.New("Blob: Not Found")
	ErrNotSupported = errors.New("Blob: Not supported by this store")
)

type Key string

type Store interface {
	Put(ctx context.Context, r io.Reader) (Key, int64, error)
	Get(ctx context.Context, key Key) (io.ReadCloser, error)
	Exists(ctx context.Context, key Key) (bool, error)
	PresignGet(ctx context.Context, key Key, ttl time.Duration) (string, error)
}