package sessiontest

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/AdityaVKochar/valence/pkg/session"
)

func Run(t *testing.T, newStore func(t *testing.T) (session.Store, int64, int64)) {
	t.Run("Lifecycle", func(t *testing.T) {
		s, user, _ := newStore(t)
		ctx := context.Background()
		sess := session.Session{IDHash: hash(), UserID: user, ExpiresAt: time.Now().Add(time.Hour), UserAgent: "test", IP: "203.0.113.7"}
		if err := s.Create(ctx, sess); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, sess.IDHash)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.IDHash, sess.IDHash) || got.UserID != user || got.UserAgent != "test" || got.IP != "203.0.113.7" || got.CreatedAt.IsZero() {
			t.Fatalf("Get = %+v", got)
		}
		later := time.Now().Add(48 * time.Hour).Truncate(time.Second)
		if err := s.Touch(ctx, sess.IDHash, later); err != nil {
			t.Fatal(err)
		}
		got, _ = s.Get(ctx, sess.IDHash)
		if !got.ExpiresAt.Equal(later) {
			t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, later)
		}
		if err := s.Delete(ctx, sess.IDHash); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, sess.IDHash); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("Get after Delete = %v", err)
		}
		if err := s.Delete(ctx, sess.IDHash); err != nil {
			t.Fatalf("second Delete = %v", err)
		}
	})

	t.Run("Expired", func(t *testing.T) {
		s, user, _ := newStore(t)
		ctx := context.Background()
		sess := session.Session{IDHash: hash(), UserID: user, ExpiresAt: time.Now().Add(-time.Second)}
		if err := s.Create(ctx, sess); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, sess.IDHash); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("Get expired = %v", err)
		}
	})

	t.Run("DeleteForUser", func(t *testing.T) {
		s, alice, bob := newStore(t)
		ctx := context.Background()
		a1, a2, b1 := hash(), hash(), hash()
		for _, x := range []session.Session{
			{IDHash: a1, UserID: alice, ExpiresAt: time.Now().Add(time.Hour)},
			{IDHash: a2, UserID: alice, ExpiresAt: time.Now().Add(time.Hour)},
			{IDHash: b1, UserID: bob, ExpiresAt: time.Now().Add(time.Hour)},
		} {
			if err := s.Create(ctx, x); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.DeleteForUser(ctx, alice); err != nil {
			t.Fatal(err)
		}
		for _, h := range [][]byte{a1, a2} {
			if _, err := s.Get(ctx, h); !errors.Is(err, session.ErrNotFound) {
				t.Fatalf("alice's session survived: %v", err)
			}
		}
		if _, err := s.Get(ctx, b1); err != nil {
			t.Fatalf("bob's session was removed: %v", err)
		}
	})
}

func hash() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}
