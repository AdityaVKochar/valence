package memsession

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/AdityaVKochar/valence/pkg/session"
)

type Store struct {
	mu       sync.Mutex
	sessions map[string]session.Session
}

var _ session.Store = (*Store)(nil)

func New() *Store { return &Store{sessions: map[string]session.Session{}} }

func (s *Store) Create(_ context.Context, sess session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	sess.IDHash = slices.Clone(sess.IDHash)
	s.sessions[string(sess.IDHash)] = sess
	return nil
}

func (s *Store) Get(_ context.Context, idHash []byte) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[string(idHash)]
	if !ok || !sess.ExpiresAt.After(time.Now()) {
		return session.Session{}, session.ErrNotFound
	}
	return sess, nil
}

func (s *Store) Touch(_ context.Context, idHash []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[string(idHash)]; ok {
		sess.ExpiresAt = expiresAt
		s.sessions[string(idHash)] = sess
	}
	return nil
}

func (s *Store) Delete(_ context.Context, idHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, string(idHash))
	return nil
}

func (s *Store) DeleteForUser(_ context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, sess := range s.sessions {
		if sess.UserID == userID {
			delete(s.sessions, k)
		}
	}
	return nil
}
