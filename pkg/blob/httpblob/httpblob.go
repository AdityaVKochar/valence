package httpblob

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/AdityaVKochar/valence/pkg/blob"
)

// Store reads blobs from the API's internal /internal/blobs endpoint. It is read-only.
type Store struct {
	base   string
	token  string
	client *http.Client
}

var _ blob.Store = (*Store)(nil)

func New(baseURL, token string, client *http.Client) *Store {
	if client == nil {
		client = http.DefaultClient
	}
	return &Store{base: strings.TrimRight(baseURL, "/") + "/internal/blobs/", token: token, client: client}
}

func (s *Store) Put(context.Context, io.Reader) (blob.Key, int64, error) {
	return "", 0, blob.ErrNotSupported
}

func (s *Store) PresignGet(context.Context, blob.Key, time.Duration) (string, error) {
	return "", blob.ErrNotSupported
}

func (s *Store) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	resp, err := s.do(ctx, http.MethodGet, key)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (s *Store) Exists(ctx context.Context, key blob.Key) (bool, error) {
	resp, err := s.do(ctx, http.MethodHead, key)
	if err == blob.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return true, nil
}

func (s *Store) do(ctx context.Context, method string, key blob.Key) (*http.Response, error) {
	if !key.Valid() {
		return nil, blob.ErrNotFound
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+string(key), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp, nil
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, blob.ErrNotFound
	}
	resp.Body.Close()
	return nil, fmt.Errorf("httpblob: %s %s: %s", method, key, resp.Status)
}
