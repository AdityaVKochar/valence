package submissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/AdityaVKochar/valence/gen/go/db"
	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/v1/valencev1connect"
	"github.com/AdityaVKochar/valence/pkg/lang"
	"github.com/AdityaVKochar/valence/pkg/pg"
	"github.com/AdityaVKochar/valence/pkg/queue"
	"github.com/AdityaVKochar/valence/services/api/internal/auth"
	"github.com/AdityaVKochar/valence/services/api/internal/problems"
)

const JobKind = "judge"

type JobPayload struct {
	SubmissionID int64 `json:"submission_id"`
	Attempt      int32 `json:"attempt"`
}

type EnqueueFunc func(ctx context.Context, tx pgx.Tx, job queue.Job) (string, error)

type Options struct {
	Pool       *pgxpool.Pool
	Languages  *lang.Registry
	MaxCodeKiB int
	MaxActive  int
	Limiter    *RateLimiter
	Enqueue    EnqueueFunc
}

type Service struct {
	opts Options
	q    *db.Queries
}

var _ valencev1connect.SubmissionServiceHandler = (*Service)(nil)

func New(opts Options) *Service {
	if opts.Limiter == nil {
		opts.Limiter = NewRateLimiter(0, 1)
	}
	return &Service{opts: opts, q: db.New(opts.Pool)}
}

func (s *Service) ListLanguages(context.Context, *connect.Request[valencev1.ListLanguagesRequest]) (*connect.Response[valencev1.ListLanguagesResponse], error) {
	resp := &valencev1.ListLanguagesResponse{}
	for _, l := range s.opts.Languages.All() {
		resp.Languages = append(resp.Languages, &valencev1.Language{
			Id:             l.ID,
			DisplayName:    l.Name,
			MonacoLanguage: l.Monaco,
			SourceFile:     l.Source,
			Template:       l.Template,
		})
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) SubmitSolution(ctx context.Context, req *connect.Request[valencev1.SubmitSolutionRequest]) (*connect.Response[valencev1.SubmitSolutionResponse], error) {
	user, err := auth.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	m := req.Msg
	if _, ok := s.opts.Languages.Get(m.Language); !ok {
		return nil, invalid("unknown language %q", m.Language)
	}
	switch {
	case strings.TrimSpace(m.Source) == "":
		return nil, invalid("source is empty")
	case len(m.Source) > s.opts.MaxCodeKiB*1024:
		return nil, invalid("source is %d KiB; the limit is %d KiB", (len(m.Source)+1023)/1024, s.opts.MaxCodeKiB)
	case !utf8.ValidString(m.Source) || strings.ContainsRune(m.Source, 0):
		return nil, invalid("source must be UTF-8 text")
	}
	p, err := problems.Visible(ctx, s.q, user, m.ProblemSlug)
	if err != nil {
		return nil, err
	}
	if s.opts.MaxActive > 0 {
		active, err := s.q.CountActiveSubmissions(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if active >= int64(s.opts.MaxActive) {
			e := connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("you already have %d submissions being judged; wait for one to finish", active))
			e.Meta().Set("Retry-After", "2")
			return nil, e
		}
	}
	if ok, wait := s.opts.Limiter.Allow(user.ID); !ok {
		secs := int(math.Ceil(wait.Seconds()))
		e := connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("you are submitting too fast; try again in %d s", secs))
		e.Meta().Set("Retry-After", strconv.Itoa(secs))
		return nil, e
	}

	var id int64
	err = pg.InTx(ctx, s.opts.Pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		sub, err := q.CreateSubmission(ctx, db.CreateSubmissionParams{
			UserID:          user.ID,
			ProblemID:       p.ID,
			ProblemRevision: p.Revision,
			Language:        m.Language,
			Source:          m.Source,
		})
		if err != nil {
			return err
		}
		if err := q.CreateAttempt(ctx, db.CreateAttemptParams{SubmissionID: sub.ID, Attempt: 1}); err != nil {
			return err
		}
		payload, _ := json.Marshal(JobPayload{SubmissionID: sub.ID, Attempt: 1})
		if _, err := s.opts.Enqueue(ctx, tx, queue.Job{Kind: JobKind, Payload: payload, DedupeKey: fmt.Sprintf("judge:%d:1", sub.ID)}); err != nil {
			return fmt.Errorf("enqueue judge job: %w", err)
		}
		id = sub.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&valencev1.SubmitSolutionResponse{SubmissionId: id}), nil
}

func (s *Service) GetSubmission(ctx context.Context, req *connect.Request[valencev1.GetSubmissionRequest]) (*connect.Response[valencev1.GetSubmissionResponse], error) {
	user, err := auth.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.q.GetSubmission(ctx, req.Msg.SubmissionId)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Submission.UserID != user.ID && !auth.IsAdmin(user)) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("submission not found"))
	}
	if err != nil {
		return nil, err
	}
	sub := row.Submission
	resp := &valencev1.GetSubmissionResponse{
		Submission: toProto(sub, row.ProblemSlug, row.ProblemTitle, row.UserHandle),
		Source:     sub.Source,
	}
	attempt, err := s.q.GetAttempt(ctx, db.GetAttemptParams{SubmissionID: sub.ID, Attempt: sub.Attempt})
	if err != nil {
		return nil, err
	}
	if attempt.CompileOutput != nil {
		resp.CompileOutput = *attempt.CompileOutput
	}
	results, err := s.q.ListTestResults(ctx, db.ListTestResultsParams{SubmissionID: sub.ID, Attempt: sub.Attempt})
	if err != nil {
		return nil, err
	}
	tests, err := s.q.ListTests(ctx, sub.ProblemID)
	if err != nil {
		return nil, err
	}
	sample := map[int32]bool{}
	for _, t := range tests {
		sample[t.Ordinal] = t.IsSample
	}
	for _, r := range results {
		tr := &valencev1.TestResult{Ordinal: r.Ordinal, Verdict: VerdictToProto(r.Verdict), TimeMs: r.TimeMs, IsSample: sample[r.Ordinal]}
		if tr.IsSample {
			tr.MemoryKib = &r.MemoryKib
		}
		resp.Tests = append(resp.Tests, tr)
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) ListMySubmissions(ctx context.Context, req *connect.Request[valencev1.ListMySubmissionsRequest]) (*connect.Response[valencev1.ListMySubmissionsResponse], error) {
	user, err := auth.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	size := problems.PageSize(req.Msg.PageSize)
	before, err := problems.DecodePageToken(req.Msg.PageToken, "s")
	if err != nil {
		return nil, err
	}
	if before == 0 {
		before = math.MaxInt64
	}
	var problemID *int64
	if req.Msg.ProblemSlug != "" {
		p, err := problems.Visible(ctx, s.q, user, req.Msg.ProblemSlug)
		if err != nil {
			return nil, err
		}
		problemID = &p.ID
	}
	rows, err := s.q.ListUserSubmissions(ctx, db.ListUserSubmissionsParams{UserID: user.ID, ProblemID: problemID, BeforeID: before, PageSize: size + 1})
	if err != nil {
		return nil, err
	}
	resp := &valencev1.ListMySubmissionsResponse{}
	if len(rows) > int(size) {
		rows = rows[:size]
		resp.NextPageToken = problems.EncodePageToken("s", rows[len(rows)-1].Submission.ID)
	}
	for _, r := range rows {
		resp.Submissions = append(resp.Submissions, toProto(r.Submission, r.ProblemSlug, r.ProblemTitle, r.UserHandle))
	}
	return connect.NewResponse(resp), nil
}

func toProto(s db.Submission, slug, title, handle string) *valencev1.Submission {
	out := &valencev1.Submission{
		Id:           s.ID,
		UserId:       s.UserID,
		UserHandle:   handle,
		ProblemSlug:  slug,
		ProblemTitle: title,
		Language:     s.Language,
		Attempt:      s.Attempt,
		Status:       StatusToProto(s.Status),
		TimeMs:       s.TimeMs,
		MemoryKib:    s.MemoryKib,
		CurrentTest:  s.CurrentTest,
		CreatedAt:    timestamppb.New(s.CreatedAt),
	}
	if s.Verdict != nil {
		out.Verdict = VerdictToProto(*s.Verdict)
	}
	if s.JudgedAt != nil {
		out.JudgedAt = timestamppb.New(*s.JudgedAt)
	}
	return out
}

func StatusToProto(s db.SubmissionStatus) valencev1.SubmissionStatus {
	switch s {
	case db.SubmissionStatusQueued:
		return valencev1.SubmissionStatus_SUBMISSION_STATUS_QUEUED
	case db.SubmissionStatusLeased:
		return valencev1.SubmissionStatus_SUBMISSION_STATUS_LEASED
	case db.SubmissionStatusCompiling:
		return valencev1.SubmissionStatus_SUBMISSION_STATUS_COMPILING
	case db.SubmissionStatusRunning:
		return valencev1.SubmissionStatus_SUBMISSION_STATUS_RUNNING
	case db.SubmissionStatusChecking:
		return valencev1.SubmissionStatus_SUBMISSION_STATUS_CHECKING
	case db.SubmissionStatusFinalized:
		return valencev1.SubmissionStatus_SUBMISSION_STATUS_FINALIZED
	case db.SubmissionStatusRetryableError:
		return valencev1.SubmissionStatus_SUBMISSION_STATUS_RETRYABLE_ERROR
	}
	return valencev1.SubmissionStatus_SUBMISSION_STATUS_UNSPECIFIED
}

var verdicts = []struct {
	db    db.Verdict
	proto valencev1.Verdict
}{
	{db.VerdictAC, valencev1.Verdict_VERDICT_ACCEPTED},
	{db.VerdictWA, valencev1.Verdict_VERDICT_WRONG_ANSWER},
	{db.VerdictTLE, valencev1.Verdict_VERDICT_TIME_LIMIT_EXCEEDED},
	{db.VerdictMLE, valencev1.Verdict_VERDICT_MEMORY_LIMIT_EXCEEDED},
	{db.VerdictRE, valencev1.Verdict_VERDICT_RUNTIME_ERROR},
	{db.VerdictOLE, valencev1.Verdict_VERDICT_OUTPUT_LIMIT_EXCEEDED},
	{db.VerdictCE, valencev1.Verdict_VERDICT_COMPILATION_ERROR},
	{db.VerdictIE, valencev1.Verdict_VERDICT_INTERNAL_ERROR},
}

func VerdictToProto(v db.Verdict) valencev1.Verdict {
	for _, x := range verdicts {
		if x.db == v {
			return x.proto
		}
	}
	return valencev1.Verdict_VERDICT_UNSPECIFIED
}

func VerdictFromProto(v valencev1.Verdict) (db.Verdict, bool) {
	for _, x := range verdicts {
		if x.proto == v {
			return x.db, true
		}
	}
	return "", false
}

func invalid(format string, args ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, args...))
}
