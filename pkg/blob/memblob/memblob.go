package memblob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"
	"time"

	"github.com/AdityaVKochar/valence/pkg/blob"
)

type Store struct {
	mu    sync.RWMutex
	blobs map[blob.Key][]byte
	gets  int
}

var _ blob.Store = (*Store)(nil)

func New() *Store { return &Store{blobs: map[blob.Key][]byte{}} }

func (s *Store) Put(_ context.Context, r io.Reader) (blob.Key, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(b)
	key := blob.Key(hex.EncodeToString(sum[:]))
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.blobs[key]; !ok {
		s.blobs[key] = b
	}
	return key, int64(len(b)), nil
}

func (s *Store) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[key]
	if !ok {
		return nil, blob.ErrNotFound
	}
	s.gets++
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *Store) Exists(_ context.Context, key blob.Key) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.blobs[key]
	return ok, nil
}

func (s *Store) PresignGet(context.Context, blob.Key, time.Duration) (string, error) {
	return "", blob.ErrNotSupported
}

func (s *Store) Gets() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gets
}
