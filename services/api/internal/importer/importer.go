package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AdityaVKochar/valence/gen/go/db"
	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/pg"
	"github.com/AdityaVKochar/valence/pkg/problempkg"
)

type Result string

const (
	Created   Result = "created"
	Updated   Result = "updated"
	Unchanged Result = "unchanged"
)

type Outcome struct {
	Slug     string
	Result   Result
	Revision int32
	Tests    int
}

type Importer struct {
	pool  *pgxpool.Pool
	q     *db.Queries
	blobs blob.Store
}

func New(pool *pgxpool.Pool, blobs blob.Store) *Importer {
	return &Importer{pool: pool, q: db.New(pool), blobs: blobs}
}

func (im *Importer) Import(ctx context.Context, p *problempkg.Package) (Outcome, error) {
	tests := make([]db.Test, 0, len(p.Tests))
	for _, t := range p.Tests {
		inKey, inSize, err := im.put(ctx, t.InputPath)
		if err != nil {
			return Outcome{}, err
		}
		outKey, outSize, err := im.put(ctx, t.OutputPath)
		if err != nil {
			return Outcome{}, err
		}
		tests = append(tests, db.Test{
			Ordinal:    int32(t.Ordinal),
			InputHash:  string(inKey),
			InputSize:  inSize,
			OutputHash: string(outKey),
			OutputSize: outSize,
			IsSample:   t.Sample,
		})
	}

	out := Outcome{Slug: p.Slug, Tests: len(tests)}
	err := pg.InTx(ctx, im.pool, func(tx pgx.Tx) error {
		q := im.q.WithTx(tx)
		existing, err := q.GetProblemBySlug(ctx, p.Slug)
		var problem db.Problem
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			problem, err = q.CreateProblem(ctx, db.CreateProblemParams{
				Slug:           p.Slug,
				Title:          p.Title,
				StatementMd:    p.Statement,
				TimeLimitMs:    p.TimeLimitMs,
				MemoryLimitKib: p.MemoryLimitKiB,
				Checker:        p.Checker,
				Visibility:     db.ProblemVisibility(p.Visibility),
			})
			if err != nil {
				return err
			}
			out.Result = Created
		case err != nil:
			return err
		default:
			current, err := q.ListTests(ctx, existing.ID)
			if err != nil {
				return err
			}
			if sameProblem(existing, p) && sameTests(existing.ID, current, tests) {
				out.Result, out.Revision = Unchanged, existing.Revision
				return nil
			}
			problem, err = q.UpdateProblem(ctx, db.UpdateProblemParams{
				Title:          p.Title,
				StatementMd:    p.Statement,
				TimeLimitMs:    p.TimeLimitMs,
				MemoryLimitKib: p.MemoryLimitKiB,
				Checker:        p.Checker,
				Visibility:     db.ProblemVisibility(p.Visibility),
				ID:             existing.ID,
			})
			if err != nil {
				return err
			}
			if err := q.DeleteTests(ctx, problem.ID); err != nil {
				return err
			}
			out.Result = Updated
		}
		for _, t := range tests {
			if err := q.InsertTest(ctx, db.InsertTestParams{
				ProblemID:  problem.ID,
				Ordinal:    t.Ordinal,
				InputHash:  t.InputHash,
				InputSize:  t.InputSize,
				OutputHash: t.OutputHash,
				OutputSize: t.OutputSize,
				IsSample:   t.IsSample,
			}); err != nil {
				return err
			}
		}
		out.Revision = problem.Revision
		return nil
	})
	return out, err
}

func (im *Importer) put(ctx context.Context, path string) (blob.Key, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	k, n, err := im.blobs.Put(ctx, f)
	if err != nil {
		return "", 0, fmt.Errorf("store %s: %w", path, err)
	}
	return k, n, nil
}

func sameProblem(e db.Problem, p *problempkg.Package) bool {
	return e.Title == p.Title && e.StatementMd == p.Statement && e.TimeLimitMs == p.TimeLimitMs &&
		e.MemoryLimitKib == p.MemoryLimitKiB && e.Checker == p.Checker && string(e.Visibility) == p.Visibility
}

func sameTests(problemID int64, current, next []db.Test) bool {
	return slices.EqualFunc(current, next, func(a, b db.Test) bool {
		b.ProblemID = problemID
		return a == b
	})
}
