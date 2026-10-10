package judge

import (
	"context"
	"errors"
	"time"

	"github.com/AdityaVKochar/valence/pkg/blob"
)

var ErrPermanent = errors.New("judge: permanent failure")

type Verdict string

const (
	Accepted            Verdict = "AC"
	WrongAnswer         Verdict = "WA"
	TimeLimitExceeded   Verdict = "TLE"
	MemoryLimitExceeded Verdict = "MLE"
	RuntimeError        Verdict = "RE"
	OutputLimitExceeded Verdict = "OLE"
	CompilationError    Verdict = "CE"
	InternalError       Verdict = "IE"
)

type Stage string

const (
	Compiling Stage = "compiling"
	Running   Stage = "running"
	Checking  Stage = "checking"
)

type Caps struct {
	Languages      []string // language IDs from judge/languages.yaml
	CustomCheckers bool
	MaxTests       int // 0 means no limit
}

type Test struct {
	Ordinal int
	Input   blob.Key
	Output  blob.Key
}

type Job struct {
	SubmissionID   int64
	Attempt        int
	Language       string // ID from judge/languages.yaml
	Source         []byte
	TimeLimit      time.Duration
	MemoryLimitKiB int64
	Checker        string // "exact", "tokens" or "float:<eps>" in P1
	Tests          []Test // in ordinal order
}

type TestResult struct {
	Ordinal   int
	Verdict   Verdict
	Time      time.Duration
	MemoryKiB int64
}

type Result struct {
	Verdict         Verdict
	CompileOutput   string // truncated to 64 KiB
	Tests           []TestResult
	MaxTime         time.Duration
	MaxMemoryKiB    int64
	Provider        string
	ToolchainDigest string
}

type Progress struct {
	Stage Stage
	Test  int
}

type Provider interface {
	Name() string
	Capabilities() Caps
	Judge(ctx context.Context, job Job, report func(Progress)) (Result, error)
	Health(ctx context.Context) error
}
