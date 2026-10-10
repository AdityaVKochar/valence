package pgsession

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AdityaVKochar/valence/gen/go/db"
	"github.com/AdityaVKochar/valence/pkg/session"
)

type Store struct {
	q *db.Queries
}

var _ session.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store { return &Store{q: db.New(pool)} }

func (s *Store) Create(ctx context.Context, sess session.Session) error {
	var ip *netip.Addr
	if a, err := netip.ParseAddr(sess.IP); err == nil {
		ip = &a
	}
	return s.q.CreateSession(ctx, db.CreateSessionParams{
		IDHash:    sess.IDHash,
		UserID:    sess.UserID,
		ExpiresAt: sess.ExpiresAt,
		UserAgent: sess.UserAgent,
		Ip:        ip,
	})
}

func (s *Store) Get(ctx context.Context, idHash []byte) (session.Session, error) {
	row, err := s.q.GetSession(ctx, idHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, session.ErrNotFound
	}
	if err != nil {
		return session.Session{}, err
	}
	sess := session.Session{
		IDHash:    row.IDHash,
		UserID:    row.UserID,
		CreatedAt: row.CreatedAt,
		ExpiresAt: row.ExpiresAt,
		UserAgent: row.UserAgent,
	}
	if row.Ip != nil {
		sess.IP = row.Ip.String()
	}
	return sess, nil
}

func (s *Store) Touch(ctx context.Context, idHash []byte, expiresAt time.Time) error {
	return s.q.TouchSession(ctx, db.TouchSessionParams{ExpiresAt: expiresAt, IDHash: idHash})
}

func (s *Store) Delete(ctx context.Context, idHash []byte) error {
	return s.q.DeleteSession(ctx, idHash)
}

func (s *Store) DeleteForUser(ctx context.Context, userID int64) error {
	return s.q.DeleteUserSessions(ctx, userID)
}

func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	return s.q.DeleteExpiredSessions(ctx)
}
