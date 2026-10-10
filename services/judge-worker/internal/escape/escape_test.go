// Package escape runs the sandbox escape cases in judge/sandbox-tests against the isolate provider.
package escape

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/AdityaVKochar/valence/pkg/blob/memblob"
	"github.com/AdityaVKochar/valence/pkg/judge"
	"github.com/AdityaVKochar/valence/pkg/lang"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/engine"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/sandbox"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/testcache"
)

const casesDir = "../../../../judge/sandbox-tests"

var languages = map[string]string{".c": "c17", ".cpp": "cpp17", ".py": "python3"}

var knownVerdicts = []judge.Verdict{
	judge.Accepted, judge.WrongAnswer, judge.TimeLimitExceeded, judge.MemoryLimitExceeded,
	judge.RuntimeError, judge.OutputLimitExceeded, judge.CompilationError,
}

type escapeCase struct {
	Name       string
	Proves     string          `yaml:"proves"`
	Verdicts   []judge.Verdict `yaml:"verdicts"`
	HostAbsent []string        `yaml:"host_absent"`
	Language   string          `yaml:"-"`
	Source     []byte          `yaml:"-"`
}

func TestMain(m *testing.M) {
	sandbox.RunHelperIfRequested()
	os.Exit(m.Run())
}

func loadCases(t *testing.T) []escapeCase {
	t.Helper()
	dirs, err := os.ReadDir(casesDir)
	if err != nil {
		t.Fatal(err)
	}
	var cases []escapeCase
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(casesDir, d.Name())
		c := escapeCase{Name: d.Name()}
		b, err := os.ReadFile(filepath.Join(dir, "case.yaml"))
		if err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("%s/case.yaml: %v", d.Name(), err)
		}
		if strings.TrimSpace(c.Proves) == "" {
			t.Errorf("%s: case.yaml needs a proves sentence", d.Name())
		}
		if len(c.Verdicts) == 0 {
			t.Errorf("%s: case.yaml needs at least one allowed verdict", d.Name())
		}
		for _, v := range c.Verdicts {
			if !slices.Contains(knownVerdicts, v) || v == judge.WrongAnswer || v == judge.CompilationError {
				t.Errorf("%s: %q cannot be an allowed verdict", d.Name(), v)
			}
		}
		for _, p := range c.HostAbsent {
			if !filepath.IsAbs(p) {
				t.Errorf("%s: host_absent path %q must be absolute", d.Name(), p)
			}
		}
		files, _ := filepath.Glob(filepath.Join(dir, "main.*"))
		if len(files) != 1 {
			t.Fatalf("%s: want exactly one main.c, main.cpp or main.py, found %v", d.Name(), files)
		}
		ext := filepath.Ext(files[0])
		if c.Language = languages[ext]; c.Language == "" {
			t.Fatalf("%s: no language for %s", d.Name(), ext)
		}
		if c.Source, err = os.ReadFile(files[0]); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		t.Fatal("no cases found")
	}
	return cases
}

func TestEscapeCases(t *testing.T) {
	cases := loadCases(t)
	if os.Getenv("VALENCE_TEST_ISOLATE") != "1" {
		t.Skip("set VALENCE_TEST_ISOLATE=1 on a Linux host with isolate to run the cases (see make sandbox-test)")
	}
	bin := os.Getenv("ISOLATE_BIN")
	if bin == "" {
		bin = "isolate"
	}
	sb, err := sandbox.NewIsolate(bin, 0, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sb.Health(ctx); err != nil {
		t.Fatalf("isolate is not usable: %v", err)
	}
	store := memblob.New()
	cache, err := testcache.New(t.TempDir(), 64<<20, store)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(sb, lang.Default(), cache, t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	in, _, err := store.Put(ctx, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := store.Put(ctx, strings.NewReader("SAFE\n"))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			for _, p := range c.HostAbsent {
				_ = os.RemoveAll(p)
			}
			if !slices.Contains(eng.Capabilities().Languages, c.Language) {
				t.Fatalf("this worker has no %s toolchain", c.Language)
			}
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			res, err := eng.Judge(ctx, judge.Job{
				SubmissionID:   1,
				Attempt:        1,
				Language:       c.Language,
				Source:         c.Source,
				TimeLimit:      2 * time.Second,
				MemoryLimitKiB: 256 << 10,
				Checker:        "tokens",
				Tests:          []judge.Test{{Ordinal: 1, Input: in, Output: out}},
			}, nil)
			if err != nil {
				t.Fatalf("judge: %v", err)
			}
			if !slices.Contains(c.Verdicts, res.Verdict) {
				t.Errorf("verdict %s, want one of %v (a WA means the case printed ESCAPED); compile output %q",
					res.Verdict, c.Verdicts, res.CompileOutput)
			}
			for _, p := range c.HostAbsent {
				if _, err := os.Lstat(p); err == nil {
					t.Errorf("%s exists on the host", p)
					_ = os.RemoveAll(p)
				}
			}
			checkHost(t)
		})
	}
}

func checkHost(t *testing.T) {
	t.Helper()
	if err := exec.Command("true").Run(); err != nil {
		t.Errorf("host cannot start processes: %v", err)
	}
	f, err := os.CreateTemp("", "valence-host-check-*")
	if err != nil {
		t.Errorf("host cannot create files: %v", err)
		return
	}
	f.Close()
	os.Remove(f.Name())
	if runtime.NumGoroutine() > 1000 {
		t.Errorf("%d goroutines are running", runtime.NumGoroutine())
	}
}
