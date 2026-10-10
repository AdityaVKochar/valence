package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/blob/memblob"
	"github.com/AdityaVKochar/valence/pkg/judge"
	"github.com/AdityaVKochar/valence/pkg/lang"
	"github.com/AdityaVKochar/valence/pkg/problempkg"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/sandbox"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/testcache"
)

func TestMain(m *testing.M) {
	sandbox.RunHelperIfRequested()
	os.Exit(m.Run())
}

func newSandbox(t *testing.T) sandbox.Sandbox {
	t.Helper()
	if os.Getenv("VALENCE_TEST_ISOLATE") == "1" {
		sb, err := sandbox.NewIsolate("isolate", 0, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := sb.Health(context.Background()); err != nil {
			t.Fatalf("isolate is not usable: %v", err)
		}
		return sb
	}
	if runtime.GOOS == "windows" {
		t.Skip("local-unsafe needs a Unix system")
	}
	sb, err := sandbox.NewUnsafe(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

func newEngine(t *testing.T, store blob.Store) *Engine {
	t.Helper()
	cache, err := testcache.New(t.TempDir(), 1<<30, store)
	if err != nil {
		t.Fatal(err)
	}
	slots := max(runtime.NumCPU()/2, 1)
	var opts []Option
	if os.Getenv("VALENCE_TEST_ISOLATE") != "1" {
		opts = append(opts, WithHostToolchains())
	}
	e, err := New(newSandbox(t), lang.Default(), cache, t.TempDir(), slots, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func jobFor(t *testing.T, store blob.Store, p *problempkg.Package, language string, source []byte) judge.Job {
	t.Helper()
	job := judge.Job{
		SubmissionID:   1,
		Attempt:        1,
		Language:       language,
		Source:         source,
		TimeLimit:      time.Duration(p.TimeLimitMs) * time.Millisecond,
		MemoryLimitKiB: int64(p.MemoryLimitKiB),
		Checker:        p.Checker,
	}
	for _, tc := range p.Tests {
		in, out := putFile(t, store, tc.InputPath), putFile(t, store, tc.OutputPath)
		job.Tests = append(job.Tests, judge.Test{Ordinal: tc.Ordinal, Input: in, Output: out})
	}
	return job
}

func putFile(t *testing.T, store blob.Store, path string) blob.Key {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	k, _, err := store.Put(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestExampleSolutionsGetTheirVerdicts(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs every sample solution")
	}
	store := memblob.New()
	e := newEngine(t, store)
	dirs, _ := filepath.Glob("../../../../problems/examples/*/problem.yaml")
	if len(dirs) == 0 {
		t.Fatal("no example problems")
	}
	for _, d := range dirs {
		p, err := problempkg.Load(filepath.Dir(d))
		if err != nil {
			t.Fatal(err)
		}
		for _, sol := range p.Solutions {
			name := p.Slug + "/" + filepath.Base(sol.Path)
			l, _ := lang.Default().Get(sol.Language)
			if !e.Available(l) {
				t.Logf("skipping %s: no %s toolchain here", name, sol.Language)
				continue
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				src, err := os.ReadFile(sol.Path)
				if err != nil {
					t.Fatal(err)
				}
				var stages []judge.Stage
				res, err := e.Judge(context.Background(), jobFor(t, store, p, sol.Language, src), func(pr judge.Progress) {
					stages = append(stages, pr.Stage)
				})
				if err != nil {
					t.Fatal(err)
				}
				if res.Verdict != sol.Expected {
					t.Fatalf("verdict %s, want %s; tests %+v; compile output %q", res.Verdict, sol.Expected, res.Tests, res.CompileOutput)
				}
				if res.Verdict == judge.CompilationError {
					if res.CompileOutput == "" || len(res.Tests) != 0 {
						t.Fatalf("CE needs a compiler message and no tests: %+v", res)
					}
					return
				}
				if res.Verdict == judge.Accepted && len(res.Tests) != len(p.Tests) {
					t.Fatalf("AC after %d of %d tests", len(res.Tests), len(p.Tests))
				}
				if res.Verdict != judge.Accepted && res.Tests[len(res.Tests)-1].Verdict != sol.Expected {
					t.Fatalf("judging did not stop at the first failure: %+v", res.Tests)
				}
				if len(stages) == 0 || stages[len(stages)-1] != judge.Running {
					t.Fatalf("stages %v", stages)
				}
				if res.Provider == "" || res.MaxTime < 0 {
					t.Fatalf("result %+v", res)
				}
			})
		}
	}
}

func TestPermanentErrors(t *testing.T) {
	e := newEngine(t, memblob.New())
	for _, job := range []judge.Job{
		{Language: "cobol", Checker: "tokens", Tests: []judge.Test{{Ordinal: 1}}},
		{Language: "python3", Checker: "custom", Tests: []judge.Test{{Ordinal: 1}}},
		{Language: "python3", Checker: "tokens"},
	} {
		if _, err := e.Judge(context.Background(), job, nil); err == nil || !strings.Contains(err.Error(), judge.ErrPermanent.Error()) {
			t.Errorf("Judge(%+v) = %v, want a permanent error", job, err)
		}
	}
}

func TestMissingTestDataIsAnInfrastructureError(t *testing.T) {
	e := newEngine(t, memblob.New())
	job := judge.Job{
		Language:       "python3",
		Source:         []byte("print(1)\n"),
		TimeLimit:      time.Second,
		MemoryLimitKiB: 256 << 10,
		Checker:        "tokens",
		Tests:          []judge.Test{{Ordinal: 1, Input: "0000000000000000000000000000000000000000000000000000000000000000", Output: "0000000000000000000000000000000000000000000000000000000000000000"}},
	}
	_, err := e.Judge(context.Background(), job, nil)
	if err == nil || strings.Contains(err.Error(), judge.ErrPermanent.Error()) {
		t.Fatalf("Judge = %v, want a retryable error", err)
	}
}
