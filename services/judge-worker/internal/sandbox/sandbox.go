package sandbox

import (
	"context"
	"time"
)

type Status int

const (
	OK Status = iota
	NonZeroExit
	Signaled
	TimedOut
	OutOfMemory
	OutputLimit
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case NonZeroExit:
		return "non-zero exit"
	case Signaled:
		return "signaled"
	case TimedOut:
		return "timed out"
	case OutOfMemory:
		return "out of memory"
	case OutputLimit:
		return "output limit"
	}
	return "unknown"
}

type Limits struct {
	CPUTime               time.Duration
	WallTime              time.Duration
	MemoryKiB             int64
	StackKiB              int64
	FileSizeKiB           int64
	Processes             int
	OpenFiles             int
	UnboundedAddressSpace bool
}

type Cmd struct {
	Args           []string
	Env            []string
	Stdin          string
	Stdout         string
	Stderr         string
	StderrToStdout bool
	Dirs           []string
	Limits         Limits
}

type Result struct {
	Status    Status
	ExitCode  int
	Signal    int
	CPUTime   time.Duration
	WallTime  time.Duration
	MemoryKiB int64
	Message   string
}

type Box interface {
	Dir() string
	Run(ctx context.Context, cmd Cmd) (Result, error)
	Close() error
}

type Sandbox interface {
	Name() string
	Open(ctx context.Context, slot int) (Box, error)
	Health(ctx context.Context) error
}
