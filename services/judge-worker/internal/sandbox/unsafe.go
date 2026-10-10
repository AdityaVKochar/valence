//go:build unix

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const helperEnv = "VALENCE_SANDBOX_HELPER"

type Unsafe struct {
	WorkDir string
	self    string
}

func NewUnsafe(workDir string) (*Unsafe, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	return &Unsafe{WorkDir: abs, self: self}, nil
}

func (s *Unsafe) Name() string { return "local-unsafe" }

func (s *Unsafe) Health(context.Context) error {
	_, err := os.Stat(s.WorkDir)
	return err
}

func (s *Unsafe) Open(_ context.Context, slot int) (Box, error) {
	dir, err := os.MkdirTemp(s.WorkDir, fmt.Sprintf("box-%d-", slot))
	if err != nil {
		return nil, err
	}
	return &unsafeBox{s: s, dir: dir}, nil
}

type unsafeBox struct {
	s   *Unsafe
	dir string
}

func (b *unsafeBox) Dir() string  { return b.dir }
func (b *unsafeBox) Close() error { return os.RemoveAll(b.dir) }

type helperLimits struct {
	CPUSeconds uint64
	FileBytes  uint64
	AddrBytes  uint64
	StackBytes uint64
	OpenFiles  uint64
}

func (b *unsafeBox) Run(ctx context.Context, cmd Cmd) (Result, error) {
	if len(cmd.Args) == 0 {
		return Result{}, errors.New("sandbox: empty command")
	}
	l := cmd.Limits
	hl := helperLimits{
		CPUSeconds: uint64(l.CPUTime/time.Second) + 1,
		FileBytes:  uint64(l.FileSizeKiB) * 1024,
		OpenFiles:  uint64(cmp0(l.OpenFiles, 64)),
	}
	if !l.UnboundedAddressSpace && l.MemoryKiB > 0 {
		hl.AddrBytes = uint64(l.MemoryKiB)*1024*2 + 1<<30
	}
	if l.StackKiB > 0 {
		hl.StackBytes = uint64(l.StackKiB) * 1024
	}
	limits, _ := json.Marshal(hl)

	wall := l.WallTime
	if wall <= 0 {
		wall = 2*l.CPUTime + time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()

	prog, err := resolve(cmd.Args[0], cmd.Env)
	if err != nil {
		return Result{}, err
	}
	c := exec.CommandContext(rctx, b.s.self, append([]string{prog}, cmd.Args[1:]...)...)
	c.Dir = b.dir
	c.Env = append([]string{helperEnv + "=" + string(limits)}, cmd.Env...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = time.Second

	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	open := func(name string, write bool) (*os.File, error) {
		if name == "" {
			return nil, nil
		}
		p := filepath.Join(b.dir, name)
		var f *os.File
		var err error
		if write {
			f, err = os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		} else {
			f, err = os.Open(p)
		}
		if err == nil {
			files = append(files, f)
		}
		return f, err
	}
	stdin, err := open(cmd.Stdin, false)
	if err != nil {
		return Result{}, err
	}
	stdout, err := open(cmd.Stdout, true)
	if err != nil {
		return Result{}, err
	}
	stderr := stdout
	if !cmd.StderrToStdout {
		if stderr, err = open(cmd.Stderr, true); err != nil {
			return Result{}, err
		}
	}
	if stdin != nil {
		c.Stdin = stdin
	}
	if stdout != nil {
		c.Stdout = stdout
	}
	if stderr != nil {
		c.Stderr = stderr
	}

	start := time.Now()
	if err := c.Start(); err != nil {
		return Result{}, fmt.Errorf("sandbox: start %s: %w", prog, err)
	}
	oom := watchMemory(c.Process, l.MemoryKiB)
	runErr := c.Wait()
	wallTime := time.Since(start)
	peak, killed := oom()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if c.ProcessState == nil {
		return Result{}, fmt.Errorf("sandbox: wait %s: %w", prog, runErr)
	}
	r := Result{WallTime: wallTime}
	if ru, ok := c.ProcessState.SysUsage().(*syscall.Rusage); ok {
		r.CPUTime = time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
		r.MemoryKiB = maxRSSKiB(ru)
	}
	r.MemoryKiB = max(r.MemoryKiB, peak)
	ws, _ := c.ProcessState.Sys().(syscall.WaitStatus)
	switch {
	case killed:
		r.Status = OutOfMemory
		r.Signal = int(syscall.SIGKILL)
	case rctx.Err() == context.DeadlineExceeded:
		r.Status = TimedOut
		r.Message = "wall time limit exceeded"
	case ws.Signaled():
		r.Signal = int(ws.Signal())
		switch ws.Signal() {
		case syscall.SIGXCPU:
			r.Status = TimedOut
		case syscall.SIGXFSZ:
			r.Status = OutputLimit
		case syscall.SIGKILL:
			if r.CPUTime >= l.CPUTime {
				r.Status = TimedOut
			} else {
				r.Status = Signaled
			}
		default:
			r.Status = Signaled
		}
	case ws.ExitStatus() == helperExecFailed:
		return Result{}, fmt.Errorf("sandbox: could not exec %s", prog)
	case ws.ExitStatus() != 0:
		r.Status = NonZeroExit
		r.ExitCode = ws.ExitStatus()
	}
	if r.Status == OK && l.CPUTime > 0 && r.CPUTime > l.CPUTime {
		r.Status = TimedOut
	}
	return r, nil
}

const helperExecFailed = 127

// watchMemory polls the resident memory of p and kills it once it goes over
// limitKiB. Without cgroups this is the closest local-unsafe gets to a memory limit: an
// allocation that is paged in slowly would otherwise run into the CPU limit first and get TLE.
// The returned function stops the watcher and reports the highest reading and whether it killed.
func watchMemory(p *os.Process, limitKiB int64) func() (peakKiB int64, killed bool) {
	if limitKiB <= 0 {
		return func() (int64, bool) { return 0, false }
	}
	if _, ok := currentRSSKiB(p.Pid); !ok {
		return func() (int64, bool) { return 0, false }
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	var peak int64
	var killed bool
	go func() {
		defer close(done)
		t := time.NewTicker(5 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			rss, ok := currentRSSKiB(p.Pid)
			if !ok {
				continue
			}
			peak = max(peak, rss)
			if rss > limitKiB {
				killed = true
				_ = p.Kill()
				return
			}
		}
	}()
	return func() (int64, bool) {
		close(stop)
		<-done
		return peak, killed
	}
}

func RunHelperIfRequested() {
	spec, ok := os.LookupEnv(helperEnv)
	if !ok {
		return
	}
	var hl helperLimits
	if err := json.Unmarshal([]byte(spec), &hl); err != nil || len(os.Args) < 2 {
		os.Exit(helperExecFailed)
	}
	set := func(res int, v uint64) {
		if v > 0 {
			_ = syscall.Setrlimit(res, &syscall.Rlimit{Cur: v, Max: v})
		}
	}
	set(syscall.RLIMIT_CPU, hl.CPUSeconds)
	set(syscall.RLIMIT_FSIZE, hl.FileBytes)
	set(syscall.RLIMIT_AS, hl.AddrBytes)
	set(syscall.RLIMIT_STACK, hl.StackBytes)
	set(syscall.RLIMIT_NOFILE, hl.OpenFiles)
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if len(e) < len(helperEnv) || e[:len(helperEnv)] != helperEnv {
			env = append(env, e)
		}
	}
	_ = syscall.Exec(os.Args[1], os.Args[1:], env)
	os.Exit(helperExecFailed)
}
