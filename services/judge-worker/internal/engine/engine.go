package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/checker"
	"github.com/AdityaVKochar/valence/pkg/judge"
	"github.com/AdityaVKochar/valence/pkg/lang"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/sandbox"
)

const (
	OutputLimitKiB      = 64 << 10
	CompileOutputLimit  = 64 << 10
	compileFileLimitKiB = 256 << 10
	compileLog          = "compile.log"
	inputFile           = "input.txt"
	outputFile          = "output.txt"
	stderrFile          = "stderr.txt"
)

type TestData interface {
	Acquire(ctx context.Context, key blob.Key) (path string, release func(), err error)
}

type Engine struct {
	hostEnv []string
	sb      sandbox.Sandbox
	langs   *lang.Registry
	data    TestData
	workDir string
	slots   chan int
	caps    judge.Caps
}

var _ judge.Provider = (*Engine)(nil)

type Option func(*Engine)

func WithHostToolchains() Option {
	return func(e *Engine) {
		e.hostEnv = []string{"PATH=" + os.Getenv("PATH")}
		rustup := os.Getenv("RUSTUP_HOME")
		if home, err := os.UserHomeDir(); rustup == "" && err == nil {
			if _, err := os.Stat(filepath.Join(home, ".rustup")); err == nil {
				rustup = filepath.Join(home, ".rustup")
			}
		}
		if rustup != "" {
			e.hostEnv = append(e.hostEnv, "RUSTUP_HOME="+rustup)
		}
	}
}

func New(sb sandbox.Sandbox, langs *lang.Registry, data TestData, workDir string, slots int, opts ...Option) (*Engine, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	e := &Engine{sb: sb, langs: langs, data: data, workDir: workDir, slots: make(chan int, slots)}
	for _, opt := range opts {
		opt(e)
	}
	for i := range slots {
		e.slots <- i
	}
	e.caps = judge.Caps{CustomCheckers: false}
	for _, l := range langs.All() {
		if e.available(l) {
			e.caps.Languages = append(e.caps.Languages, l.ID)
		}
	}
	return e, nil
}

func (e *Engine) Available(l lang.Language) bool { return e.available(l) }

func (e *Engine) available(l lang.Language) bool {
	cmd := l.Run
	if len(l.Compile) > 0 {
		cmd = l.Compile
	}
	if len(cmd) == 0 || filepath.Base(cmd[0]) != cmd[0] {
		return true
	}
	_, err := lookPath(cmd[0], e.env(l))
	return err == nil
}

func (e *Engine) env(l lang.Language) []string {
	env := l.EnvList()
	for _, kv := range e.hostEnv {
		key, _, _ := strings.Cut(kv, "=")
		env = slices.DeleteFunc(env, func(x string) bool { return strings.HasPrefix(x, key+"=") })
		env = append(env, kv)
	}
	return env
}

func (e *Engine) Name() string                     { return e.sb.Name() }
func (e *Engine) Capabilities() judge.Caps         { return e.caps }
func (e *Engine) Health(ctx context.Context) error { return e.sb.Health(ctx) }

func (e *Engine) Judge(ctx context.Context, job judge.Job, report func(judge.Progress)) (judge.Result, error) {
	if report == nil {
		report = func(judge.Progress) {}
	}
	l, ok := e.langs.Get(job.Language)
	if !ok {
		return judge.Result{}, fmt.Errorf("%w: unknown language %q", judge.ErrPermanent, job.Language)
	}
	if !e.available(l) {
		return judge.Result{}, fmt.Errorf("%w: no toolchain for %s on this worker", judge.ErrPermanent, l.ID)
	}
	chk, err := checker.Parse(job.Checker)
	if err != nil {
		return judge.Result{}, fmt.Errorf("%w: %v", judge.ErrPermanent, err)
	}
	if len(job.Tests) == 0 {
		return judge.Result{}, fmt.Errorf("%w: problem has no tests", judge.ErrPermanent)
	}

	var slot int
	select {
	case slot = <-e.slots:
	case <-ctx.Done():
		return judge.Result{}, ctx.Err()
	}
	defer func() { e.slots <- slot }()

	artifacts := filepath.Join(e.workDir, "slot-"+strconv.Itoa(slot))
	if err := os.RemoveAll(artifacts); err != nil {
		return judge.Result{}, err
	}
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		return judge.Result{}, err
	}
	defer os.RemoveAll(artifacts)

	res := judge.Result{Provider: e.Name(), Verdict: judge.Accepted}
	ce, err := e.compile(ctx, slot, l, job.Source, artifacts, report)
	if err != nil {
		return judge.Result{}, err
	}
	if ce != nil {
		res.Verdict = judge.CompilationError
		res.CompileOutput = *ce
		return res, nil
	}

	for _, t := range job.Tests {
		report(judge.Progress{Stage: judge.Running, Test: t.Ordinal})
		tr, err := e.runTest(ctx, slot, l, job, chk, t, artifacts)
		if err != nil {
			return judge.Result{}, fmt.Errorf("test %d: %w", t.Ordinal, err)
		}
		res.Tests = append(res.Tests, tr)
		res.MaxTime = max(res.MaxTime, tr.Time)
		res.MaxMemoryKiB = max(res.MaxMemoryKiB, tr.MemoryKiB)
		if tr.Verdict != judge.Accepted {
			res.Verdict = tr.Verdict
			break
		}
	}
	return res, nil
}

func (e *Engine) compile(ctx context.Context, slot int, l lang.Language, source []byte, artifacts string, report func(judge.Progress)) (*string, error) {
	box, err := e.sb.Open(ctx, slot)
	if err != nil {
		return nil, err
	}
	defer box.Close()
	if err := os.WriteFile(filepath.Join(box.Dir(), l.Source), source, 0o644); err != nil {
		return nil, err
	}
	if len(l.Compile) > 0 {
		report(judge.Progress{Stage: judge.Compiling})
		limit := time.Duration(l.CompileTimeLimitMs) * time.Millisecond
		r, err := box.Run(ctx, sandbox.Cmd{
			Args:           l.Compile,
			Env:            e.env(l),
			Dirs:           l.Dirs,
			Stdout:         compileLog,
			StderrToStdout: true,
			Limits: sandbox.Limits{
				CPUTime:               limit,
				WallTime:              2*limit + time.Second,
				MemoryKiB:             l.CompileMemoryMiB << 10,
				FileSizeKiB:           compileFileLimitKiB,
				Processes:             l.CompileProcesses,
				OpenFiles:             256,
				UnboundedAddressSpace: true,
			},
		})
		if err != nil {
			return nil, err
		}
		if r.Status != sandbox.OK {
			out := readLimited(filepath.Join(box.Dir(), compileLog), CompileOutputLimit)
			switch r.Status {
			case sandbox.TimedOut:
				out += "\nCompilation timed out."
			case sandbox.OutOfMemory:
				out += "\nCompilation ran out of memory."
			case sandbox.OutputLimit:
				out += "\nCompilation produced too much output."
			}
			return &out, nil
		}
		_ = os.Remove(filepath.Join(box.Dir(), compileLog))
	}
	return nil, copyTree(box.Dir(), artifacts)
}

func (e *Engine) runTest(ctx context.Context, slot int, l lang.Language, job judge.Job, chk checker.Checker, t judge.Test, artifacts string) (judge.TestResult, error) {
	tr := judge.TestResult{Ordinal: t.Ordinal}
	input, releaseIn, err := e.data.Acquire(ctx, t.Input)
	if err != nil {
		return tr, err
	}
	defer releaseIn()

	box, err := e.sb.Open(ctx, slot)
	if err != nil {
		return tr, err
	}
	defer box.Close()
	if err := copyTree(artifacts, box.Dir()); err != nil {
		return tr, err
	}
	if err := copyFile(input, filepath.Join(box.Dir(), inputFile), 0o644); err != nil {
		return tr, err
	}

	cpu := l.RunTimeLimit(job.TimeLimit)
	mem := l.RunMemoryKiB(job.MemoryLimitKiB)
	r, err := box.Run(ctx, sandbox.Cmd{
		Args:   l.RunArgs(job.MemoryLimitKiB),
		Env:    e.env(l),
		Dirs:   l.Dirs,
		Stdin:  inputFile,
		Stdout: outputFile,
		Stderr: stderrFile,
		Limits: sandbox.Limits{
			CPUTime:               cpu,
			WallTime:              2*cpu + time.Second,
			MemoryKiB:             mem,
			StackKiB:              mem,
			FileSizeKiB:           OutputLimitKiB,
			Processes:             l.RunProcesses,
			OpenFiles:             64,
			UnboundedAddressSpace: l.UnboundedAddressSpace,
		},
	})
	if err != nil {
		return tr, err
	}
	tr.Time = min(r.CPUTime, cpu+cpu/10)
	tr.MemoryKiB = r.MemoryKiB
	switch {
	case r.Status == sandbox.OutOfMemory || r.MemoryKiB > mem:
		tr.Verdict = judge.MemoryLimitExceeded
	case r.Status == sandbox.TimedOut || r.CPUTime > cpu:
		tr.Verdict = judge.TimeLimitExceeded
	case r.Status == sandbox.OutputLimit:
		tr.Verdict = judge.OutputLimitExceeded
	case r.Status != sandbox.OK:
		tr.Verdict = judge.RuntimeError
	}
	if tr.Verdict != "" {
		return tr, nil
	}
	out := filepath.Join(box.Dir(), outputFile)
	if st, err := os.Stat(out); err == nil && st.Size() >= OutputLimitKiB<<10 {
		tr.Verdict = judge.OutputLimitExceeded
		return tr, nil
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(out, nil, 0o644); err != nil {
			return tr, err
		}
	}
	expected, releaseOut, err := e.data.Acquire(ctx, t.Output)
	if err != nil {
		return tr, err
	}
	defer releaseOut()
	cr, err := checker.CheckFiles(chk, expected, out)
	if err != nil {
		return tr, err
	}
	if cr.OK {
		tr.Verdict = judge.Accepted
	} else {
		tr.Verdict = judge.WrongAnswer
	}
	return tr, nil
}

func readLimited(path string, limit int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, int64(limit)))
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode().IsRegular():
			return copyFile(p, target, info.Mode().Perm()|0o444)
		}
		return nil
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
