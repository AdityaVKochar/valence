// Package e2e starts the real api and judge-worker binaries against Postgres and checks that
// every sample solution of the aplusb problem gets its expected verdict through the public API.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/v1/valencev1connect"
	"github.com/AdityaVKochar/valence/pkg/judge"
	"github.com/AdityaVKochar/valence/pkg/pg/pgtest"
	"github.com/AdityaVKochar/valence/pkg/problempkg"
)

const (
	root    = "../.."
	problem = root + "/problems/examples/aplusb"
)

// Languages whose toolchains every CI runner and contributor machine has.
var languages = map[string]bool{"c17": true, "cpp17": true, "python3": true}

var verdicts = map[valencev1.Verdict]judge.Verdict{
	valencev1.Verdict_VERDICT_ACCEPTED:              judge.Accepted,
	valencev1.Verdict_VERDICT_WRONG_ANSWER:          judge.WrongAnswer,
	valencev1.Verdict_VERDICT_TIME_LIMIT_EXCEEDED:   judge.TimeLimitExceeded,
	valencev1.Verdict_VERDICT_MEMORY_LIMIT_EXCEEDED: judge.MemoryLimitExceeded,
	valencev1.Verdict_VERDICT_RUNTIME_ERROR:         judge.RuntimeError,
	valencev1.Verdict_VERDICT_OUTPUT_LIMIT_EXCEEDED: judge.OutputLimitExceeded,
	valencev1.Verdict_VERDICT_COMPILATION_ERROR:     judge.CompilationError,
}

func TestSubmitAndJudge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local-unsafe needs a Unix system")
	}
	dsn := pgtest.NewURL(t)
	pkg, err := problempkg.Load(problem)
	if err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	build(t, bin, "api", "valence-admin", "judge-worker")
	data := t.TempDir()
	apiAddr, internalAddr, workerAddr := freeAddr(t), freeAddr(t), freeAddr(t)
	apiURL := "http://" + apiAddr
	env := []string{
		"VALENCE_ENV=dev",
		"DATABASE_URL=" + dsn,
		"LOG_LEVEL=debug",
		"API_ADDR=" + apiAddr,
		"API_INTERNAL_ADDR=" + internalAddr,
		"PUBLIC_API_URL=" + apiURL,
		"BLOB_DIR=" + filepath.Join(data, "blobs"),
		"AUTO_MIGRATE=true",
		"SUBMIT_COOLDOWN=0s",
		"MAX_ACTIVE_SUBMISSIONS=0",
		"WORKER_ADDR=" + workerAddr,
		"API_INTERNAL_URL=http://" + internalAddr,
		"WORKER_SLOTS=2",
		"JUDGE_PROVIDER=local-unsafe",
		"JUDGE_CACHE_DIR=" + filepath.Join(data, "cache"),
		"JUDGE_WORK_DIR=" + filepath.Join(data, "work"),
	}

	start(t, env, filepath.Join(bin, "api"))
	waitHealthy(t, apiURL)
	admin := exec.Command(filepath.Join(bin, "valence-admin"), "problem", "import", problem)
	admin.Env = append(os.Environ(), env...)
	if out, err := admin.CombinedOutput(); err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	start(t, env, filepath.Join(bin, "judge-worker"))
	waitHealthy(t, "http://"+workerAddr)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(apiURL + "/auth/dev?user=e2e-runner")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("dev login: %s", resp.Status)
	}
	subs := valencev1connect.NewSubmissionServiceClient(client, apiURL)
	ctx := context.Background()

	type submitted struct {
		name string
		id   int64
		want judge.Verdict
	}
	var all []submitted
	for _, sol := range pkg.Solutions {
		if !languages[sol.Language] {
			continue
		}
		src, err := os.ReadFile(sol.Path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := subs.SubmitSolution(ctx, connect.NewRequest(&valencev1.SubmitSolutionRequest{
			ProblemSlug: pkg.Slug,
			Language:    sol.Language,
			Source:      string(src),
		}))
		if err != nil {
			t.Fatalf("submit %s: %v", filepath.Base(sol.Path), err)
		}
		all = append(all, submitted{filepath.Base(sol.Path), r.Msg.SubmissionId, sol.Expected})
	}
	seen := map[judge.Verdict]bool{}
	for _, s := range all {
		seen[s.want] = true
	}
	for _, v := range []judge.Verdict{judge.Accepted, judge.WrongAnswer, judge.TimeLimitExceeded,
		judge.MemoryLimitExceeded, judge.RuntimeError, judge.CompilationError} {
		if !seen[v] {
			t.Fatalf("aplusb has no C, C++ or Python solution expecting %s", v)
		}
	}

	deadline := time.Now().Add(3 * time.Minute)
	for _, s := range all {
		t.Run(s.name, func(t *testing.T) {
			for {
				r, err := subs.GetSubmission(ctx, connect.NewRequest(&valencev1.GetSubmissionRequest{SubmissionId: s.id}))
				if err != nil {
					t.Fatal(err)
				}
				sub := r.Msg.Submission
				if sub.Status == valencev1.SubmissionStatus_SUBMISSION_STATUS_FINALIZED {
					if got := verdicts[sub.Verdict]; got != s.want {
						t.Fatalf("verdict %s (%s), want %s; compile output %q", got, sub.Verdict, s.want, r.Msg.CompileOutput)
					}
					if s.want == judge.Accepted && len(r.Msg.Tests) != len(pkg.Tests) {
						t.Fatalf("AC with %d of %d test results", len(r.Msg.Tests), len(pkg.Tests))
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("still %s after the deadline", sub.Status)
				}
				time.Sleep(200 * time.Millisecond)
			}
		})
	}

	mine, err := subs.ListMySubmissions(ctx, connect.NewRequest(&valencev1.ListMySubmissionsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(mine.Msg.Submissions) != len(all) {
		t.Fatalf("ListMySubmissions returned %d submissions, want %d", len(mine.Msg.Submissions), len(all))
	}
}

func build(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		pkg := "./services/api/cmd/" + name
		if name == "judge-worker" {
			pkg = "./services/judge-worker/cmd/judge-worker"
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", pkg, err, out)
		}
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// syncBuffer collects a process's output so it can be printed when the test fails.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func start(t *testing.T, env []string, path string) {
	t.Helper()
	var out syncBuffer
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("%s output:\n%s", filepath.Base(path), indent(out.String()))
		}
	})
}

func waitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			err = fmt.Errorf("status %s", resp.Status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not become ready: %v", base, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ")
}
