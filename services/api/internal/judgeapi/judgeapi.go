package judgeapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AdityaVKochar/valence/gen/go/db"
	judgev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1/judgev1connect"
	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/pg"
	"github.com/AdityaVKochar/valence/services/api/internal/submissions"
)

const maxCompileOutput = 64 << 10

type Service struct {
	pool *pgxpool.Pool
	q    *db.Queries
	log  *slog.Logger
}

var _ judgev1connect.JudgeServiceHandler = (*Service)(nil)

func New(pool *pgxpool.Pool, log *slog.Logger) *Service {
	return &Service{pool: pool, q: db.New(pool), log: log}
}

func (s *Service) GetJob(ctx context.Context, req *connect.Request[judgev1.GetJobRequest]) (*connect.Response[judgev1.GetJobResponse], error) {
	row, err := s.q.GetJudgeJob(ctx, db.GetJudgeJobParams{SubmissionID: req.Msg.SubmissionId, Attempt: req.Msg.Attempt})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("submission %d attempt %d not found", req.Msg.SubmissionId, req.Msg.Attempt))
	}
	if err != nil {
		return nil, err
	}
	if row.AttemptStatus == db.SubmissionStatusFinalized || row.CurrentAttempt != req.Msg.Attempt {
		return connect.NewResponse(&judgev1.GetJobResponse{AlreadyFinalized: true}), nil
	}
	tests, err := s.q.ListTests(ctx, row.ProblemID)
	if err != nil {
		return nil, err
	}
	job := &judgev1.JudgeJob{
		SubmissionId:   row.ID,
		Attempt:        req.Msg.Attempt,
		Language:       row.Language,
		Source:         []byte(row.Source),
		TimeLimitMs:    row.TimeLimitMs,
		MemoryLimitKib: row.MemoryLimitKib,
		Checker:        row.Checker,
	}
	for _, t := range tests {
		job.Tests = append(job.Tests, &judgev1.JudgeTest{
			Ordinal:    t.Ordinal,
			InputKey:   t.InputHash,
			InputSize:  t.InputSize,
			OutputKey:  t.OutputHash,
			OutputSize: t.OutputSize,
		})
	}
	return connect.NewResponse(&judgev1.GetJobResponse{Job: job}), nil
}

var stageStatus = map[judgev1.Stage]db.SubmissionStatus{
	judgev1.Stage_STAGE_LEASED:          db.SubmissionStatusLeased,
	judgev1.Stage_STAGE_COMPILING:       db.SubmissionStatusCompiling,
	judgev1.Stage_STAGE_RUNNING:         db.SubmissionStatusRunning,
	judgev1.Stage_STAGE_CHECKING:        db.SubmissionStatusChecking,
	judgev1.Stage_STAGE_RETRYABLE_ERROR: db.SubmissionStatusRetryableError,
}

func ValidTransition(from, to db.SubmissionStatus) bool {
	switch to {
	case db.SubmissionStatusLeased, db.SubmissionStatusRetryableError:
		return from != db.SubmissionStatusFinalized
	case db.SubmissionStatusCompiling:
		return from == db.SubmissionStatusLeased || from == db.SubmissionStatusCompiling
	case db.SubmissionStatusRunning:
		return from == db.SubmissionStatusLeased || from == db.SubmissionStatusCompiling || from == db.SubmissionStatusRunning || from == db.SubmissionStatusChecking
	case db.SubmissionStatusChecking:
		return from == db.SubmissionStatusRunning || from == db.SubmissionStatusChecking
	case db.SubmissionStatusFinalized:
		return from != db.SubmissionStatusFinalized && from != db.SubmissionStatusQueued
	}
	return false
}

func (s *Service) ReportProgress(ctx context.Context, req *connect.Request[judgev1.ReportProgressRequest]) (*connect.Response[judgev1.ReportProgressResponse], error) {
	m := req.Msg
	status, ok := stageStatus[m.Stage]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown stage %v", m.Stage))
	}
	accepted := false
	err := pg.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		a, err := q.LockAttempt(ctx, db.LockAttemptParams{SubmissionID: m.SubmissionId, Attempt: m.Attempt})
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, errors.New("attempt not found"))
		}
		if err != nil {
			return err
		}
		if a.Status == db.SubmissionStatusFinalized {
			return nil
		}
		if !ValidTransition(a.Status, status) {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("cannot move from %s to %s", a.Status, status))
		}
		if _, err := q.SetAttemptProgress(ctx, db.SetAttemptProgressParams{
			Status:       status,
			Provider:     optional(m.Provider),
			Worker:       optional(m.Worker),
			SubmissionID: m.SubmissionId,
			Attempt:      m.Attempt,
		}); err != nil {
			return err
		}
		var current *int32
		if status == db.SubmissionStatusRunning || status == db.SubmissionStatusChecking {
			current = &m.Test
		}
		if _, err := q.SetSubmissionProgress(ctx, db.SetSubmissionProgressParams{Status: status, CurrentTest: current, ID: m.SubmissionId, Attempt: m.Attempt}); err != nil {
			return err
		}
		accepted = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&judgev1.ReportProgressResponse{Accepted: accepted}), nil
}

func (s *Service) ReportResult(ctx context.Context, req *connect.Request[judgev1.ReportResultRequest]) (*connect.Response[judgev1.ReportResultResponse], error) {
	m := req.Msg
	verdict, ok := submissions.VerdictFromProto(m.Verdict)
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("verdict is required"))
	}
	type result struct {
		ordinal int32
		verdict db.Verdict
		time    int32
		memory  int32
	}
	var results []result
	for _, t := range m.Tests {
		v, ok := submissions.VerdictFromProto(t.Verdict)
		if !ok || t.Ordinal < 1 || t.TimeMs < 0 || t.MemoryKib < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid result for test %d", t.Ordinal))
		}
		results = append(results, result{t.Ordinal, v, t.TimeMs, t.MemoryKib})
	}
	applied := false
	err := pg.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		a, err := q.LockAttempt(ctx, db.LockAttemptParams{SubmissionID: m.SubmissionId, Attempt: m.Attempt})
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, errors.New("attempt not found"))
		}
		if err != nil {
			return err
		}
		if a.Status == db.SubmissionStatusFinalized {
			return nil
		}
		for _, r := range results {
			if err := q.InsertTestResult(ctx, db.InsertTestResultParams{
				SubmissionID: m.SubmissionId,
				Attempt:      m.Attempt,
				Ordinal:      r.ordinal,
				Verdict:      r.verdict,
				TimeMs:       r.time,
				MemoryKib:    r.memory,
			}); err != nil {
				return err
			}
		}
		var timeMs, memKiB *int32
		if len(results) > 0 {
			timeMs, memKiB = &m.TimeMs, &m.MemoryKib
		}
		if _, err := q.FinalizeAttempt(ctx, db.FinalizeAttemptParams{
			Verdict:       &verdict,
			TimeMs:        timeMs,
			MemoryKib:     memKiB,
			CompileOutput: optional(cleanText(m.CompileOutput, maxCompileOutput)),
			Provider:      optional(m.Provider),
			Worker:        optional(m.Worker),
			InfraRetries:  m.InfraRetries,
			LastError:     optional(cleanText(m.Error, 4096)),
			SubmissionID:  m.SubmissionId,
			Attempt:       m.Attempt,
		}); err != nil {
			return err
		}
		if _, err := q.FinalizeSubmission(ctx, db.FinalizeSubmissionParams{
			Verdict:   &verdict,
			TimeMs:    timeMs,
			MemoryKib: memKiB,
			ID:        m.SubmissionId,
			Attempt:   m.Attempt,
		}); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if applied {
		s.log.Info("Submission judged", "submission_id", m.SubmissionId, "attempt", m.Attempt, "verdict", verdict, "worker", m.Worker)
	}
	return connect.NewResponse(&judgev1.ReportResultResponse{Applied: applied}), nil
}

func BlobHandler(store blob.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := blob.Key(r.PathValue("key"))
		if !key.Valid() {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodHead {
			ok, err := store.Exists(r.Context(), key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			} else if !ok {
				http.NotFound(w, r)
			}
			return
		}
		rc, err := store.Get(r.Context(), key)
		if errors.Is(err, blob.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"`+string(key)+`"`)
		_, _ = io.Copy(w, rc)
	})
}

func RequireToken(token string, next http.Handler, open ...string) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range open {
			if r.URL.Path == p {
				next.ServeHTTP(w, r)
				return
			}
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func cleanText(s string, limit int) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "�")
	if len(s) > limit {
		s = s[:limit]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
