package localblob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/AdityaVKochar/valence/pkg/blob"
)

type Store struct {
	dir string
}

var _ blob.Store = (*Store)(nil)

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Put(ctx context.Context, r io.Reader) (blob.Key, int64, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "put-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), &ctxReader{ctx: ctx, r: r})
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	key := blob.Key(hex.EncodeToString(h.Sum(nil)))
	dst := s.path(key)
	if _, err := os.Stat(dst); err == nil {
		return key, size, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Chmod(tmp.Name(), 0o444); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", 0, err
	}
	return key, size, nil
}

func (s *Store) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	if !key.Valid() {
		return nil, blob.ErrNotFound
	}
	f, err := os.Open(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, blob.ErrNotFound
	}
	return f, err
}

func (s *Store) Exists(_ context.Context, key blob.Key) (bool, error) {
	if !key.Valid() {
		return false, nil
	}
	_, err := os.Stat(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) PresignGet(context.Context, blob.Key, time.Duration) (string, error) {
	return "", blob.ErrNotSupported
}

func (s *Store) path(key blob.Key) string {
	k := string(key)
	return filepath.Join(s.dir, k[:2], k[2:4], k)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
