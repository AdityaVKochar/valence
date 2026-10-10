package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	judgev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1/judgev1connect"
	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/v1/valencev1connect"
	"github.com/AdityaVKochar/valence/pkg/blob/memblob"
	"github.com/AdityaVKochar/valence/pkg/config"
	"github.com/AdityaVKochar/valence/pkg/pg/pgtest"
	"github.com/AdityaVKochar/valence/pkg/problempkg"
	"github.com/AdityaVKochar/valence/pkg/queue"
	"github.com/AdityaVKochar/valence/services/api/internal/auth"
	"github.com/AdityaVKochar/valence/services/api/internal/importer"
)

const webOrigin = "http://web.test"

type env struct {
	t        *testing.T
	pool     *pgxpool.Pool
	public   *httptest.Server
	internal *httptest.Server
	cfg      config.API
}

func newEnv(t *testing.T, mutate func(*config.API, *Options)) *env {
	t.Helper()
	pool := pgtest.New(t)
	blobs := memblob.New()
	ctx := context.Background()
	im := importer.New(pool, blobs)
	for _, slug := range []string{"aplusb", "reverse-string", "sum-of-array"} {
		p, err := problempkg.Load("../../../../problems/examples/" + slug)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := im.Import(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE problems SET visibility = 'private' WHERE slug = 'reverse-string'"); err != nil {
		t.Fatal(err)
	}
	cfg := config.API{
		Common:               config.Common{Env: config.Dev, InternalToken: "secret-token"},
		WebOrigin:            webOrigin,
		PublicURL:            "http://api.test",
		SessionTTL:           24 * time.Hour,
		MaxCodeKiB:           64,
		SubmitCooldown:       0,
		MaxActiveSubmissions: 0,
	}
	opts := Options{Pool: pool, Blobs: blobs, Logger: slog.New(slog.DiscardHandler), Providers: []auth.Provider{}}
	if mutate != nil {
		mutate(&cfg, &opts)
	}
	opts.Config = cfg
	a := New(opts)
	e := &env{t: t, pool: pool, cfg: cfg, public: httptest.NewServer(a.Public), internal: httptest.NewServer(a.Internal)}
	t.Cleanup(e.public.Close)
	t.Cleanup(e.internal.Close)
	return e
}

func (e *env) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *env) login(handle, role string) *http.Client {
	e.t.Helper()
	c := e.client()
	resp, err := c.Get(e.public.URL + "/auth/dev?redirect=0&user=" + handle + "&role=" + role)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("dev login: %s %s", resp.Status, b)
	}
	return c
}

func (e *env) users(c *http.Client) valencev1connect.UserServiceClient {
	return valencev1connect.NewUserServiceClient(c, e.public.URL)
}

func (e *env) problems(c *http.Client) valencev1connect.ProblemServiceClient {
	return valencev1connect.NewProblemServiceClient(c, e.public.URL)
}

func (e *env) submissions(c *http.Client) valencev1connect.SubmissionServiceClient {
	return valencev1connect.NewSubmissionServiceClient(c, e.public.URL)
}

func (e *env) judge(token string) judgev1connect.JudgeServiceClient {
	return judgev1connect.NewJudgeServiceClient(http.DefaultClient, e.internal.URL, connect.WithInterceptors(connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Set("Authorization", "Bearer "+token)
				return next(ctx, req)
			}
		})))
}

func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	if connect.CodeOf(err) != code {
		t.Fatalf("got %v, want %v", err, code)
	}
}

func TestSessionLifecycle(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()

	_, err := e.users(e.client()).GetMe(ctx, connect.NewRequest(&valencev1.GetMeRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)

	c := e.login("alice", "")
	me, err := e.users(c).GetMe(ctx, connect.NewRequest(&valencev1.GetMeRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if me.Msg.User.Handle != "alice" || me.Msg.User.Role != valencev1.Role_ROLE_CONTESTANT {
		t.Fatalf("GetMe = %+v", me.Msg.User)
	}
	again := e.login("alice", "admin")
	me2, err := e.users(again).GetMe(ctx, connect.NewRequest(&valencev1.GetMeRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if me2.Msg.User.Id != me.Msg.User.Id || me2.Msg.User.Role != valencev1.Role_ROLE_ADMIN {
		t.Fatalf("second login made a different user or kept the role: %+v", me2.Msg.User)
	}

	req, _ := http.NewRequest(http.MethodPost, e.public.URL+"/auth/logout", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %s", resp.Status)
	}
	_, err = e.users(c).GetMe(ctx, connect.NewRequest(&valencev1.GetMeRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)

	forged := e.client()
	u, _ := url.Parse(e.public.URL)
	forged.Jar.SetCookies(u, []*http.Cookie{{Name: auth.CookieName, Value: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}})
	_, err = e.users(forged).GetMe(ctx, connect.NewRequest(&valencev1.GetMeRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
}

func TestDevLoginOnlyInDev(t *testing.T) {
	e := newEnv(t, func(c *config.API, _ *Options) {
		c.Env = config.Prod
	})
	resp, err := http.Get(e.public.URL + "/auth/dev?redirect=0&user=mallory&role=admin")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/auth/dev in prod: %s", resp.Status)
	}
	providers, err := valencev1connect.NewAuthServiceClient(http.DefaultClient, e.public.URL).
		ListAuthProviders(context.Background(), connect.NewRequest(&valencev1.ListAuthProvidersRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(providers.Msg.Providers) != 0 {
		t.Fatalf("providers in prod with nothing configured: %v", providers.Msg.Providers)
	}
}

func TestGitHubLogin(t *testing.T) {
	var challenge string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			_ = r.ParseForm()
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				http.Error(w, `{"error":"bad_verification_code"}`, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"gho_test","token_type":"bearer"}`)
		case "/user":
			if r.Header.Get("Authorization") != "Bearer gho_test" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"id":4242,"login":"Octo.Cat","name":"The Octocat","avatar_url":"https://avatars.test/1"}`)
		case "/user/emails":
			_, _ = io.WriteString(w, `[{"email":"old@x.test","primary":false,"verified":true},{"email":"octo@x.test","primary":true,"verified":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	provider := auth.NewGitHub("client-id", "client-secret", "http://api.test/auth/github/callback")
	provider.OAuth.Endpoint = oauth2.Endpoint{AuthURL: gh.URL + "/login/oauth/authorize", TokenURL: gh.URL + "/login/oauth/access_token", AuthStyle: oauth2.AuthStyleInParams}
	provider.APIURL = gh.URL

	e := newEnv(t, func(_ *config.API, o *Options) { o.Providers = []auth.Provider{provider} })
	ctx := context.Background()
	c := e.client()

	start := func() url.Values {
		resp, err := c.Get(e.public.URL + "/auth/github/login?next=/problems/aplusb")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil || resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc.String(), gh.URL) {
			t.Fatalf("login redirect: %s %q", resp.Status, resp.Header.Get("Location"))
		}
		q := loc.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("state") == "" {
			t.Fatalf("authorize URL lacks PKCE or state: %s", loc)
		}
		challenge = q.Get("code_challenge")
		return q
	}
	callback := func(state, code string) *http.Response {
		resp, err := c.Get(e.public.URL + "/auth/github/callback?state=" + url.QueryEscape(state) + "&code=" + code)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	q := start()
	if resp := callback("forged", "good-code"); resp.Header.Get("Location") != webOrigin+"/login?error=invalid_state" {
		t.Fatalf("forged state redirected to %q", resp.Header.Get("Location"))
	}
	q = start()
	if resp := callback(q.Get("state"), "bad-code"); resp.Header.Get("Location") != webOrigin+"/login?error=provider_error" {
		t.Fatalf("bad code redirected to %q", resp.Header.Get("Location"))
	}
	q = start()
	if resp := callback(q.Get("state"), "good-code"); resp.Header.Get("Location") != webOrigin+"/problems/aplusb" {
		t.Fatalf("successful login redirected to %q", resp.Header.Get("Location"))
	}
	me, err := e.users(c).GetMe(ctx, connect.NewRequest(&valencev1.GetMeRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if me.Msg.User.Handle != "Octo-Cat" || me.Msg.User.DisplayName != "The Octocat" || me.Msg.User.AvatarUrl != "https://avatars.test/1" {
		t.Fatalf("GetMe = %+v", me.Msg.User)
	}
	var email string
	if err := e.pool.QueryRow(ctx, "SELECT email FROM users WHERE id = $1", me.Msg.User.Id).Scan(&email); err != nil || email != "octo@x.test" {
		t.Fatalf("email = %q, %v", email, err)
	}
	if resp := callback(q.Get("state"), "good-code"); resp.Header.Get("Location") != webOrigin+"/login?error=invalid_state" {
		t.Fatalf("replayed callback redirected to %q", resp.Header.Get("Location"))
	}
}

func TestProblemVisibility(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	anon := e.problems(http.DefaultClient)

	var slugs []string
	token := ""
	for range 5 {
		resp, err := anon.ListProblems(ctx, connect.NewRequest(&valencev1.ListProblemsRequest{PageSize: 1, PageToken: token}))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range resp.Msg.Problems {
			slugs = append(slugs, p.Slug)
		}
		if token = resp.Msg.NextPageToken; token == "" {
			break
		}
	}
	if strings.Join(slugs, ",") != "aplusb,sum-of-array" {
		t.Fatalf("anonymous listing = %v", slugs)
	}
	_, err := anon.ListProblems(ctx, connect.NewRequest(&valencev1.ListProblemsRequest{PageToken: "garbage"}))
	wantCode(t, err, connect.CodeInvalidArgument)

	_, err = e.problems(e.login("carol", "")).GetProblem(ctx, connect.NewRequest(&valencev1.GetProblemRequest{Slug: "reverse-string"}))
	wantCode(t, err, connect.CodeNotFound)
	if _, err := e.problems(e.login("sam", "setter")).GetProblem(ctx, connect.NewRequest(&valencev1.GetProblemRequest{Slug: "reverse-string"})); err != nil {
		t.Fatalf("setter cannot see a private problem: %v", err)
	}

	resp, err := anon.GetProblem(ctx, connect.NewRequest(&valencev1.GetProblemRequest{Slug: "aplusb"}))
	if err != nil {
		t.Fatal(err)
	}
	p := resp.Msg.Problem
	pkg, _ := problempkg.Load("../../../../problems/examples/aplusb")
	var samples int
	for _, tc := range pkg.Tests {
		if tc.Sample {
			samples++
		}
	}
	if len(p.Samples) != samples || int(p.TestCount) != len(pkg.Tests) || samples == len(pkg.Tests) {
		t.Fatalf("got %d samples and %d tests; package has %d samples of %d tests", len(p.Samples), p.TestCount, samples, len(pkg.Tests))
	}
	if p.Samples[0].Input == "" || p.Samples[0].Output == "" || p.StatementMd == "" {
		t.Fatalf("sample or statement missing: %+v", p)
	}
}

func TestSubmitAndJudge(t *testing.T) {
	e := newEnv(t, func(c *config.API, _ *Options) { c.SubmitCooldown = time.Hour })
	ctx := context.Background()
	alice := e.submissions(e.login("alice", ""))
	submit := func(c valencev1connect.SubmissionServiceClient, slug, lang, src string) (int64, error) {
		resp, err := c.SubmitSolution(ctx, connect.NewRequest(&valencev1.SubmitSolutionRequest{ProblemSlug: slug, Language: lang, Source: src}))
		if err != nil {
			return 0, err
		}
		return resp.Msg.SubmissionId, nil
	}

	_, err := submit(e.submissions(http.DefaultClient), "aplusb", "cpp17", "int main(){}")
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = submit(alice, "aplusb", "cobol", "x")
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = submit(alice, "aplusb", "cpp17", "   ")
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = submit(alice, "aplusb", "cpp17", strings.Repeat("x", 65*1024))
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = submit(alice, "reverse-string", "cpp17", "int main(){}")
	wantCode(t, err, connect.CodeNotFound)

	id, err := submit(alice, "aplusb", "python3", "print(sum(map(int, input().split())))")
	if err != nil {
		t.Fatal(err)
	}
	_, err = submit(alice, "aplusb", "python3", "print(1)")
	wantCode(t, err, connect.CodeResourceExhausted)
	if ce := new(connect.Error); !errors.As(err, &ce) || ce.Meta().Get("Retry-After") == "" {
		t.Fatalf("rate limit error has no Retry-After: %v", err)
	}
	var jobs int
	_ = e.pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE dedupe_key = $1", "judge:"+itoa(id)+":1").Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("judge jobs enqueued = %d", jobs)
	}

	_, err = e.submissions(e.login("bob", "")).GetSubmission(ctx, connect.NewRequest(&valencev1.GetSubmissionRequest{SubmissionId: id}))
	wantCode(t, err, connect.CodeNotFound)
	if _, err := e.submissions(e.login("root", "admin")).GetSubmission(ctx, connect.NewRequest(&valencev1.GetSubmissionRequest{SubmissionId: id})); err != nil {
		t.Fatalf("admin cannot read a submission: %v", err)
	}

	_, err = e.judge("wrong").GetJob(ctx, connect.NewRequest(&judgev1.GetJobRequest{SubmissionId: id, Attempt: 1}))
	wantCode(t, err, connect.CodeUnauthenticated)
	j := e.judge("secret-token")
	job, err := j.GetJob(ctx, connect.NewRequest(&judgev1.GetJobRequest{SubmissionId: id, Attempt: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if job.Msg.Job.Language != "python3" || len(job.Msg.Job.Tests) == 0 || job.Msg.Job.Checker == "" {
		t.Fatalf("job = %+v", job.Msg.Job)
	}
	blobResp, err := http.Get(e.internal.URL + "/internal/blobs/" + job.Msg.Job.Tests[0].InputKey)
	if err != nil {
		t.Fatal(err)
	}
	blobResp.Body.Close()
	if blobResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("blob without token: %s", blobResp.Status)
	}

	progress := func(stage judgev1.Stage, test int32) (bool, error) {
		resp, err := j.ReportProgress(ctx, connect.NewRequest(&judgev1.ReportProgressRequest{SubmissionId: id, Attempt: 1, Stage: stage, Test: test, Worker: "w1"}))
		if err != nil {
			return false, err
		}
		return resp.Msg.Accepted, nil
	}
	_, err = progress(judgev1.Stage_STAGE_COMPILING, 0)
	wantCode(t, err, connect.CodeFailedPrecondition)
	for _, s := range []judgev1.Stage{judgev1.Stage_STAGE_LEASED, judgev1.Stage_STAGE_COMPILING, judgev1.Stage_STAGE_RUNNING} {
		if ok, err := progress(s, 1); err != nil || !ok {
			t.Fatalf("progress %v: %v %v", s, ok, err)
		}
	}
	running, err := alice.GetSubmission(ctx, connect.NewRequest(&valencev1.GetSubmissionRequest{SubmissionId: id}))
	if err != nil {
		t.Fatal(err)
	}
	if running.Msg.Submission.Status != valencev1.SubmissionStatus_SUBMISSION_STATUS_RUNNING || running.Msg.Submission.GetCurrentTest() != 1 {
		t.Fatalf("submission while running = %+v", running.Msg.Submission)
	}

	result := &judgev1.ReportResultRequest{SubmissionId: id, Attempt: 1, Verdict: valencev1.Verdict_VERDICT_ACCEPTED, TimeMs: 30, MemoryKib: 9000, Worker: "w1", Provider: "local-unsafe"}
	for _, tc := range job.Msg.Job.Tests {
		result.Tests = append(result.Tests, &judgev1.JudgeTestResult{Ordinal: tc.Ordinal, Verdict: valencev1.Verdict_VERDICT_ACCEPTED, TimeMs: 10, MemoryKib: 9000})
	}
	if r, err := j.ReportResult(ctx, connect.NewRequest(result)); err != nil || !r.Msg.Applied {
		t.Fatalf("first result: %v %v", r, err)
	}
	replay := &judgev1.ReportResultRequest{SubmissionId: id, Attempt: 1, Verdict: valencev1.Verdict_VERDICT_WRONG_ANSWER}
	if r, err := j.ReportResult(ctx, connect.NewRequest(replay)); err != nil || r.Msg.Applied {
		t.Fatalf("replayed result: %v %v", r, err)
	}
	if ok, err := progress(judgev1.Stage_STAGE_RUNNING, 2); err != nil || ok {
		t.Fatalf("progress after finalize: %v %v", ok, err)
	}
	if again, err := j.GetJob(ctx, connect.NewRequest(&judgev1.GetJobRequest{SubmissionId: id, Attempt: 1})); err != nil || !again.Msg.AlreadyFinalized {
		t.Fatalf("GetJob after finalize: %v %v", again, err)
	}

	done, err := alice.GetSubmission(ctx, connect.NewRequest(&valencev1.GetSubmissionRequest{SubmissionId: id}))
	if err != nil {
		t.Fatal(err)
	}
	s := done.Msg.Submission
	if s.Status != valencev1.SubmissionStatus_SUBMISSION_STATUS_FINALIZED || s.Verdict != valencev1.Verdict_VERDICT_ACCEPTED || s.GetTimeMs() != 30 || s.JudgedAt == nil {
		t.Fatalf("judged submission = %+v", s)
	}
	if len(done.Msg.Tests) != len(job.Msg.Job.Tests) {
		t.Fatalf("got %d test results", len(done.Msg.Tests))
	}
	for _, tr := range done.Msg.Tests {
		if tr.IsSample != (tr.MemoryKib != nil) {
			t.Fatalf("test %d: sample=%v memory=%v; memory must show only on samples", tr.Ordinal, tr.IsSample, tr.MemoryKib)
		}
	}
	mine, err := alice.ListMySubmissions(ctx, connect.NewRequest(&valencev1.ListMySubmissionsRequest{}))
	if err != nil || len(mine.Msg.Submissions) != 1 || mine.Msg.Submissions[0].Id != id {
		t.Fatalf("ListMySubmissions: %v %v", mine, err)
	}
	solved, err := e.problems(e.login("alice", "")).ListProblems(ctx, connect.NewRequest(&valencev1.ListProblemsRequest{}))
	if err != nil || !solved.Msg.Problems[0].Solved || solved.Msg.Problems[1].Solved {
		t.Fatalf("solved flags: %v %v", solved, err)
	}
}

func TestSubmitRollsBackWhenEnqueueFails(t *testing.T) {
	e := newEnv(t, func(c *config.API, o *Options) {
		o.Enqueue = func(context.Context, pgx.Tx, queue.Job) (string, error) { return "", errors.New("queue down") }
	})
	ctx := context.Background()
	_, err := e.submissions(e.login("alice", "")).SubmitSolution(ctx, connect.NewRequest(&valencev1.SubmitSolutionRequest{ProblemSlug: "aplusb", Language: "cpp17", Source: "int main(){}"}))
	if err == nil {
		t.Fatal("submit succeeded with a broken queue")
	}
	var subs, attempts int
	_ = e.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM submissions), (SELECT count(*) FROM submission_attempts)").Scan(&subs, &attempts)
	if subs != 0 || attempts != 0 {
		t.Fatalf("left %d submissions and %d attempts behind", subs, attempts)
	}
}

func TestActiveSubmissionCap(t *testing.T) {
	e := newEnv(t, func(c *config.API, _ *Options) { c.MaxActiveSubmissions = 2 })
	ctx := context.Background()
	alice := e.submissions(e.login("alice", ""))
	for i := range 3 {
		_, err := alice.SubmitSolution(ctx, connect.NewRequest(&valencev1.SubmitSolutionRequest{ProblemSlug: "aplusb", Language: "cpp17", Source: "int main(){}"}))
		if i < 2 && err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			wantCode(t, err, connect.CodeResourceExhausted)
		}
	}
}

func TestMetricsAndHealth(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := e.users(e.client()).GetMe(context.Background(), connect.NewRequest(&valencev1.GetMeRequest{})); err == nil {
		t.Fatal("expected Unauthenticated")
	}
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		resp, err := http.Get(e.internal.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %s", path, resp.Status)
		}
		if path == "/metrics" && !strings.Contains(string(b), `valence_rpc_requests_total{code="unauthenticated",procedure="/valence.v1.UserService/GetMe"} 1`) {
			t.Fatalf("metrics do not count the GetMe call:\n%s", b)
		}
	}
	ping, err := valencev1connect.NewHealthServiceClient(http.DefaultClient, e.public.URL).Ping(context.Background(), connect.NewRequest(&valencev1.PingRequest{}))
	if err != nil || !strings.Contains(ping.Msg.DatabaseVersion, "PostgreSQL") {
		t.Fatalf("Ping: %v %v", ping, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
