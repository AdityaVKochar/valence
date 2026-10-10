package pgsession

import (
	"context"
	"testing"

	"github.com/AdityaVKochar/valence/gen/go/db"
	"github.com/AdityaVKochar/valence/pkg/pg/pgtest"
	"github.com/AdityaVKochar/valence/pkg/session"
	"github.com/AdityaVKochar/valence/pkg/session/sessiontest"
)

func TestConformance(t *testing.T) {
	sessiontest.Run(t, func(t *testing.T) (session.Store, int64, int64) {
		pool := pgtest.New(t)
		q := db.New(pool)
		var ids []int64
		for _, h := range []string{"alice", "bob"} {
			u, err := q.CreateUser(context.Background(), db.CreateUserParams{Handle: h, Role: db.UserRoleContestant})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, u.ID)
		}
		return New(pool), ids[0], ids[1]
	})
}
