package lang

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	judgefiles "github.com/AdityaVKochar/valence/judge"
)

type Language struct {
	ID                    string            `yaml:"id"`
	Name                  string            `yaml:"name"`
	Monaco                string            `yaml:"monaco"`
	Source                string            `yaml:"source"`
	Compile               []string          `yaml:"compile"`
	Run                   []string          `yaml:"run"`
	Env                   map[string]string `yaml:"env"`
	TimeMultiplier        float64           `yaml:"time_multiplier"`
	MemoryOverheadMiB     int64             `yaml:"memory_overhead_mib"`
	CompileTimeLimitMs    int64             `yaml:"compile_time_limit_ms"`
	CompileMemoryMiB      int64             `yaml:"compile_memory_mib"`
	CompileProcesses      int               `yaml:"compile_processes"`
	RunProcesses          int               `yaml:"run_processes"`
	UnboundedAddressSpace bool              `yaml:"unbounded_address_space"`
	Template              string            `yaml:"template"`
}

type Registry struct {
	langs []Language
}

var (
	validID     = regexp.MustCompile(`^[a-z0-9_.+-]{1,32}$`)
	validSource = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

func Default() *Registry {
	r, err := Parse(judgefiles.LanguagesYAML)
	if err != nil {
		panic(fmt.Sprintf("judge/languages.yaml: %v", err))
	}
	return r
}

func Parse(b []byte) (*Registry, error) {
	var doc struct {
		Languages []Language `yaml:"languages"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var errs []error
	seen := map[string]bool{}
	for i := range doc.Languages {
		l := &doc.Languages[i]
		l.applyDefaults()
		if err := l.validate(); err != nil {
			errs = append(errs, err)
		}
		if seen[l.ID] {
			errs = append(errs, fmt.Errorf("language %q is defined twice", l.ID))
		}
		seen[l.ID] = true
	}
	if len(doc.Languages) == 0 {
		errs = append(errs, errors.New("no languages defined"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &Registry{langs: doc.Languages}, nil
}

func (r *Registry) Get(id string) (Language, bool) {
	for _, l := range r.langs {
		if l.ID == id {
			return l, true
		}
	}
	return Language{}, false
}

func (r *Registry) All() []Language { return slices.Clone(r.langs) }

func (r *Registry) IDs() []string {
	ids := make([]string, len(r.langs))
	for i, l := range r.langs {
		ids[i] = l.ID
	}
	return ids
}

func (l *Language) applyDefaults() {
	if l.TimeMultiplier == 0 {
		l.TimeMultiplier = 1
	}
	if l.CompileTimeLimitMs == 0 {
		l.CompileTimeLimitMs = 15000
	}
	if l.CompileMemoryMiB == 0 {
		l.CompileMemoryMiB = 512
	}
	if l.CompileProcesses == 0 {
		l.CompileProcesses = 16
	}
	if l.RunProcesses == 0 {
		l.RunProcesses = 1
	}
}

func (l Language) validate() error {
	var errs []string
	if !validID.MatchString(l.ID) {
		errs = append(errs, "id must match "+validID.String())
	}
	if l.Name == "" {
		errs = append(errs, "name is required")
	}
	if !validSource.MatchString(l.Source) {
		errs = append(errs, "source must be a plain file name")
	}
	if len(l.Run) == 0 {
		errs = append(errs, "run is required")
	}
	if l.TimeMultiplier < 0.5 || l.TimeMultiplier > 10 {
		errs = append(errs, "time_multiplier must be between 0.5 and 10")
	}
	if l.MemoryOverheadMiB < 0 || l.CompileMemoryMiB < 64 || l.CompileTimeLimitMs < 1000 {
		errs = append(errs, "memory and time limits are out of range")
	}
	if len(errs) > 0 {
		return fmt.Errorf("language %q: %s", l.ID, strings.Join(errs, "; "))
	}
	return nil
}

func (l Language) RunTimeLimit(problemLimit time.Duration) time.Duration {
	return time.Duration(float64(problemLimit) * l.TimeMultiplier)
}

func (l Language) RunMemoryKiB(problemLimitKiB int64) int64 {
	return problemLimitKiB + l.MemoryOverheadMiB*1024
}

func (l Language) RunArgs(problemLimitKiB int64) []string {
	return expand(l.Run, problemLimitKiB)
}

func expand(args []string, memKiB int64) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "{memory_mib}", strconv.FormatInt(max(memKiB/1024, 16), 10))
	}
	return out
}

func (l Language) EnvList() []string {
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	keys := make([]string, 0, len(l.Env))
	for k := range l.Env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		env = append(env, k+"="+l.Env[k])
	}
	return env
}
