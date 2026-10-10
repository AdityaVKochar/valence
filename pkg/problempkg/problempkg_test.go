package problempkg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdityaVKochar/valence/pkg/judge"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"problem.yaml":        "slug: aplusb\ntitle: A + B\ntime_limit_ms: 1000\nmemory_limit_mib: 256\nchecker: tokens\nvisibility: public\nsamples: [1]\n",
		"statement.md":        "Add two numbers.",
		"tests/01.in":         "1 2\n",
		"tests/01.out":        "3\n",
		"tests/2.in":          "5 5\n",
		"tests/2.out":         "10\n",
		"solutions/ac.cpp":    "",
		"solutions/wa_off.py": "",
	})
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Slug != "aplusb" || p.MemoryLimitKiB != 256*1024 || len(p.Tests) != 2 || !p.Tests[0].Sample || p.Tests[1].Sample {
		t.Fatalf("unexpected package %+v", p)
	}
	if len(p.Solutions) != 2 || p.Solutions[0].Language != "cpp17" || p.Solutions[1].Expected != judge.WrongAnswer {
		t.Fatalf("solutions %+v", p.Solutions)
	}
}

func TestLoadListsEveryProblem(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"problem.yaml":       "slug: Bad Slug\ntitle: ''\ntime_limit_ms: 50\nmemory_limit_mib: 4096\nchecker: custom\nsamples: [9]\n",
		"tests/01.in":        "",
		"tests/03.in":        "",
		"tests/03.out":       "",
		"tests/readme.txt":   "",
		"solutions/x.cpp":    "",
		"solutions/ac.java2": "",
	})
	_, err := Load(dir)
	var v *ValidationError
	if !errors.As(err, &v) {
		t.Fatalf("Load = %v, want ValidationError", err)
	}
	msg := err.Error()
	for _, want := range []string{"slug", "title", "time_limit_ms", "memory_limit_mib", "checker", "statement.md", "samples: test 9", "readme.txt", "without gaps", "solutions/x.cpp", "unknown extension"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q:\n%s", want, msg)
		}
	}
}

func TestExamplesAreValid(t *testing.T) {
	dirs, _ := filepath.Glob("../../problems/examples/*/problem.yaml")
	if len(dirs) == 0 {
		t.Fatal("no example problems found")
	}
	for _, d := range dirs {
		if _, err := Load(filepath.Dir(d)); err != nil {
			t.Error(err)
		}
	}
}
