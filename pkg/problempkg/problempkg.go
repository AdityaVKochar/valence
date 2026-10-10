package problempkg

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/AdityaVKochar/valence/pkg/checker"
	"github.com/AdityaVKochar/valence/pkg/judge"
)

const (
	MaxStatementBytes = 256 << 10
	MaxTests          = 500
	MaxTestBytes      = 256 << 20
)

var (
	validSlug   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
	testName    = regexp.MustCompile(`^([0-9]{1,4})\.(in|out)$`)
	solutionExt = map[string]string{
		".cpp":  "cpp17",
		".c":    "c17",
		".py":   "python3",
		".java": "java21",
		".kt":   "kotlin",
		".go":   "go",
		".rs":   "rust",
	}
	verdicts = map[string]judge.Verdict{
		"ac": judge.Accepted, "wa": judge.WrongAnswer, "tle": judge.TimeLimitExceeded,
		"mle": judge.MemoryLimitExceeded, "re": judge.RuntimeError, "ole": judge.OutputLimitExceeded,
		"ce": judge.CompilationError,
	}
)

type Manifest struct {
	Slug           string `yaml:"slug"`
	Title          string `yaml:"title"`
	TimeLimitMs    int    `yaml:"time_limit_ms"`
	MemoryLimitMiB int    `yaml:"memory_limit_mib"`
	Checker        string `yaml:"checker"`
	Visibility     string `yaml:"visibility"`
	Samples        []int  `yaml:"samples"`
}

type Test struct {
	Ordinal    int
	InputPath  string
	OutputPath string
	Sample     bool
}

type Solution struct {
	Path     string
	Language string
	Expected judge.Verdict
}

type Package struct {
	Dir            string
	Slug           string
	Title          string
	Statement      string
	TimeLimitMs    int32
	MemoryLimitKiB int32
	Checker        string
	Visibility     string
	Tests          []Test
	Solutions      []Solution
}

type ValidationError struct {
	Dir      string
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %d problem(s):\n  - %s", e.Dir, len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

func Load(dir string) (*Package, error) {
	v := &ValidationError{Dir: dir}
	fail := func(format string, args ...any) { v.Problems = append(v.Problems, fmt.Sprintf(format, args...)) }
	p := &Package{Dir: dir}

	var m Manifest
	raw, err := os.ReadFile(filepath.Join(dir, "problem.yaml"))
	if err != nil {
		fail("problem.yaml: %v", err)
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(&m); err != nil {
			fail("problem.yaml: %v", err)
		}
	}
	if m.Visibility == "" {
		m.Visibility = "private"
	}
	if m.Checker == "" {
		m.Checker = "tokens"
	}
	if !validSlug.MatchString(m.Slug) {
		fail("slug %q must be 2 to 64 characters of a-z, 0-9 and '-', starting with a letter or digit", m.Slug)
	}
	if n := utf8.RuneCountInString(m.Title); n < 1 || n > 200 {
		fail("title must be 1 to 200 characters")
	}
	if m.TimeLimitMs < 100 || m.TimeLimitMs > 20000 {
		fail("time_limit_ms %d must be between 100 and 20000", m.TimeLimitMs)
	}
	if m.MemoryLimitMiB < 16 || m.MemoryLimitMiB > 2048 {
		fail("memory_limit_mib %d must be between 16 and 2048", m.MemoryLimitMiB)
	}
	if _, err := checker.Parse(m.Checker); err != nil {
		fail("checker: %v", err)
	}
	if m.Visibility != "public" && m.Visibility != "private" {
		fail("visibility %q must be public or private", m.Visibility)
	}
	p.Slug, p.Title, p.Checker, p.Visibility = m.Slug, m.Title, m.Checker, m.Visibility
	p.TimeLimitMs, p.MemoryLimitKiB = int32(m.TimeLimitMs), int32(m.MemoryLimitMiB*1024)

	statement, err := os.ReadFile(filepath.Join(dir, "statement.md"))
	switch {
	case err != nil:
		fail("statement.md: %v", err)
	case len(statement) > MaxStatementBytes:
		fail("statement.md is %d bytes; the limit is %d", len(statement), MaxStatementBytes)
	case !utf8.Valid(statement):
		fail("statement.md is not valid UTF-8")
	default:
		p.Statement = string(statement)
	}

	p.Tests = loadTests(dir, fail)
	ordinals := map[int]bool{}
	for _, t := range p.Tests {
		ordinals[t.Ordinal] = true
	}
	if len(m.Samples) == 0 && len(p.Tests) > 0 {
		fail("samples: list at least one test to show in the statement")
	}
	for _, s := range m.Samples {
		if !ordinals[s] {
			fail("samples: test %d does not exist", s)
		}
	}
	for i := range p.Tests {
		p.Tests[i].Sample = slices.Contains(m.Samples, p.Tests[i].Ordinal)
	}

	p.Solutions = loadSolutions(dir, fail)
	if len(v.Problems) > 0 {
		return nil, v
	}
	return p, nil
}

func loadTests(dir string, fail func(string, ...any)) []Test {
	entries, err := os.ReadDir(filepath.Join(dir, "tests"))
	if err != nil {
		fail("tests/: %v", err)
		return nil
	}
	ins, outs := map[int]string{}, map[int]string{}
	for _, e := range entries {
		name := e.Name()
		match := testName.FindStringSubmatch(name)
		if match == nil || e.IsDir() {
			fail("tests/%s: test files must be named NN.in and NN.out", name)
			continue
		}
		n, _ := strconv.Atoi(match[1])
		path := filepath.Join(dir, "tests", name)
		if info, err := e.Info(); err == nil && info.Size() > MaxTestBytes {
			fail("tests/%s is larger than %d MiB", name, MaxTestBytes>>20)
		}
		target := ins
		if match[2] == "out" {
			target = outs
		}
		if _, dup := target[n]; dup {
			fail("tests/%s: test %d has two %s files", name, n, match[2])
		}
		target[n] = path
	}
	var nums []int
	for n := range ins {
		nums = append(nums, n)
	}
	for n := range outs {
		if _, ok := ins[n]; !ok {
			fail("test %d has an .out file but no .in file", n)
		}
	}
	sort.Ints(nums)
	var tests []Test
	for i, n := range nums {
		if n != i+1 {
			fail("tests must be numbered 1 to N without gaps; found %d where %d was expected", n, i+1)
			break
		}
		out, ok := outs[n]
		if !ok {
			fail("test %d has an .in file but no .out file", n)
			continue
		}
		tests = append(tests, Test{Ordinal: n, InputPath: ins[n], OutputPath: out})
	}
	if len(nums) == 0 {
		fail("tests/: at least one test is required")
	}
	if len(nums) > MaxTests {
		fail("tests/: %d tests; the limit is %d", len(nums), MaxTests)
	}
	return tests
}

func loadSolutions(dir string, fail func(string, ...any)) []Solution {
	entries, err := os.ReadDir(filepath.Join(dir, "solutions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		fail("solutions/: %v", err)
		return nil
	}
	var out []Solution
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		language, ok := solutionExt[ext]
		if !ok {
			fail("solutions/%s: unknown extension %q", name, ext)
			continue
		}
		parts := strings.FieldsFunc(strings.TrimSuffix(name, ext), func(r rune) bool { return r == '_' || r == '-' || r == '.' })
		var verdict judge.Verdict
		if len(parts) > 0 {
			verdict, ok = verdicts[strings.ToLower(parts[0])]
		} else {
			ok = false
		}
		if !ok {
			fail("solutions/%s: name must start with the expected verdict (ac, wa, tle, mle, re, ole, ce)", name)
			continue
		}
		out = append(out, Solution{Path: filepath.Join(dir, "solutions", name), Language: language, Expected: verdict})
	}
	return out
}
