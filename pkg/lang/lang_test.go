package lang

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultRegistry(t *testing.T) {
	r := Default()
	for _, id := range []string{"cpp17", "python3", "c17", "java21", "go", "rust", "kotlin", "pypy3"} {
		l, ok := r.Get(id)
		if !ok {
			t.Fatalf("language %s missing", id)
		}
		if l.Template == "" || l.Monaco == "" {
			t.Errorf("%s: template and monaco id are required for the editor", id)
		}
	}
	if _, ok := r.Get("cobol"); ok {
		t.Fatal("unexpected language")
	}
}

func TestExpandAndLimits(t *testing.T) {
	l, _ := Default().Get("java21")
	args := l.RunArgs(256 * 1024)
	if !strings.Contains(strings.Join(args, " "), "-Xmx256m") {
		t.Fatalf("args %v", args)
	}
	if got := l.RunTimeLimit(time.Second); got != 1500*time.Millisecond {
		t.Fatalf("time limit %v", got)
	}
	if got := l.RunMemoryKiB(256 * 1024); got != (256+64)*1024 {
		t.Fatalf("memory %d", got)
	}
}

func TestParseRejectsBadEntries(t *testing.T) {
	_, err := Parse([]byte(`
languages:
  - id: Bad ID
    source: ../main.c
  - id: cpp
    name: C++
    source: main.cpp
    run: [./main]
  - id: cpp
    name: C++ again
    source: main.cpp
    run: [./main]
`))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"id must match", "source must be", "run is required", "defined twice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if _, err := Parse([]byte("languages:\n  - id: x\n    typo: 1\n")); err == nil {
		t.Fatal("unknown fields should be rejected")
	}
}
