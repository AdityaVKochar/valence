package problems

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AdityaVKochar/valence/gen/go/db"
	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/v1/valencev1connect"
	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/services/api/internal/auth"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
	maxSampleBytes  = 64 << 10
)

type Service struct {
	q     *db.Queries
	blobs blob.Store
}

var _ valencev1connect.ProblemServiceHandler = (*Service)(nil)

func New(pool *pgxpool.Pool, blobs blob.Store) *Service {
	return &Service{q: db.New(pool), blobs: blobs}
}

func (s *Service) ListProblems(ctx context.Context, req *connect.Request[valencev1.ListProblemsRequest]) (*connect.Response[valencev1.ListProblemsResponse], error) {
	user, _ := auth.UserFrom(ctx)
	size := PageSize(req.Msg.PageSize)
	after, err := DecodePageToken(req.Msg.PageToken, "p")
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListProblems(ctx, db.ListProblemsParams{AfterID: after, IncludePrivate: auth.IsStaff(user), PageSize: size + 1})
	if err != nil {
		return nil, err
	}
	resp := &valencev1.ListProblemsResponse{}
	if len(rows) > int(size) {
		rows = rows[:size]
		resp.NextPageToken = EncodePageToken("p", rows[len(rows)-1].ID)
	}
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	solved, err := s.solved(ctx, user, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		resp.Problems = append(resp.Problems, &valencev1.ProblemSummary{
			Id:             r.ID,
			Slug:           r.Slug,
			Title:          r.Title,
			TimeLimitMs:    r.TimeLimitMs,
			MemoryLimitKib: r.MemoryLimitKib,
			Visibility:     VisibilityToProto(r.Visibility),
			Solved:         solved[r.ID],
		})
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) GetProblem(ctx context.Context, req *connect.Request[valencev1.GetProblemRequest]) (*connect.Response[valencev1.GetProblemResponse], error) {
	user, _ := auth.UserFrom(ctx)
	p, err := Visible(ctx, s.q, user, req.Msg.Slug)
	if err != nil {
		return nil, err
	}
	samples, err := s.q.ListSampleTests(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	count, err := s.q.CountTests(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	solved, err := s.solved(ctx, user, []int64{p.ID})
	if err != nil {
		return nil, err
	}
	out := &valencev1.Problem{
		Id:             p.ID,
		Slug:           p.Slug,
		Title:          p.Title,
		StatementMd:    p.StatementMd,
		TimeLimitMs:    p.TimeLimitMs,
		MemoryLimitKib: p.MemoryLimitKib,
		Checker:        p.Checker,
		Revision:       p.Revision,
		Visibility:     VisibilityToProto(p.Visibility),
		TestCount:      int32(count),
		Solved:         solved[p.ID],
	}
	for _, t := range samples {
		in, inTrunc, err := s.readSample(ctx, t.InputHash)
		if err != nil {
			return nil, err
		}
		ans, outTrunc, err := s.readSample(ctx, t.OutputHash)
		if err != nil {
			return nil, err
		}
		out.Samples = append(out.Samples, &valencev1.SampleTest{Ordinal: t.Ordinal, Input: in, Output: ans, Truncated: inTrunc || outTrunc})
	}
	return connect.NewResponse(&valencev1.GetProblemResponse{Problem: out}), nil
}

func (s *Service) readSample(ctx context.Context, key string) (string, bool, error) {
	rc, err := s.blobs.Get(ctx, blob.Key(key))
	if err != nil {
		return "", false, connect.NewError(connect.CodeInternal, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxSampleBytes+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(b) > maxSampleBytes
	if truncated {
		b = b[:maxSampleBytes]
	}
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
		truncated = true
	}
	return string(b), truncated, nil
}

func (s *Service) solved(ctx context.Context, user *db.User, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if user == nil || len(ids) == 0 {
		return out, nil
	}
	solved, err := s.q.ListSolvedProblemIDs(ctx, db.ListSolvedProblemIDsParams{UserID: user.ID, ProblemIds: ids})
	if err != nil {
		return nil, err
	}
	for _, id := range solved {
		out[id] = true
	}
	return out, nil
}

func Visible(ctx context.Context, q *db.Queries, user *db.User, slug string) (db.Problem, error) {
	p, err := q.GetProblemBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && p.Visibility != db.ProblemVisibilityPublic && !auth.IsStaff(user)) {
		return db.Problem{}, connect.NewError(connect.CodeNotFound, errors.New("problem not found"))
	}
	return p, err
}

func VisibilityToProto(v db.ProblemVisibility) valencev1.ProblemVisibility {
	switch v {
	case db.ProblemVisibilityPublic:
		return valencev1.ProblemVisibility_PROBLEM_VISIBILITY_PUBLIC
	case db.ProblemVisibilityPrivate:
		return valencev1.ProblemVisibility_PROBLEM_VISIBILITY_PRIVATE
	}
	return valencev1.ProblemVisibility_PROBLEM_VISIBILITY_UNSPECIFIED
}

func PageSize(requested int32) int32 {
	switch {
	case requested <= 0:
		return defaultPageSize
	case requested > maxPageSize:
		return maxPageSize
	}
	return requested
}

func EncodePageToken(prefix string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(prefix + ":" + strconv.FormatInt(id, 10)))
}

func DecodePageToken(token, prefix string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err == nil {
		if v, ok := strings.CutPrefix(string(b), prefix+":"); ok {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
				return id, nil
			}
		}
	}
	return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page_token"))
}
