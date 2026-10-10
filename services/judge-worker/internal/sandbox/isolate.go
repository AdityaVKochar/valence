package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultPath = "/usr/local/bin:/usr/bin:/bin"

type Isolate struct {
	Bin     string
	BoxBase int
	MetaDir string
}

func NewIsolate(bin string, boxBase int, metaDir string) (*Isolate, error) {
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return nil, err
	}
	return &Isolate{Bin: bin, BoxBase: boxBase, MetaDir: metaDir}, nil
}

func (s *Isolate) Name() string { return "local-isolate" }

func (s *Isolate) Health(ctx context.Context) error {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return errors.New("isolate needs cgroups v2 (/sys/fs/cgroup/cgroup.controllers is missing)")
	}
	out, err := exec.CommandContext(ctx, s.Bin, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s --version: %v: %s", s.Bin, err, bytes.TrimSpace(out))
	}
	box, err := s.Open(ctx, 0)
	if err != nil {
		return err
	}
	return box.Close()
}

func (s *Isolate) Open(ctx context.Context, slot int) (Box, error) {
	id := s.BoxBase + slot
	_ = exec.CommandContext(ctx, s.Bin, "--cg", "--box-id="+strconv.Itoa(id), "--cleanup").Run()
	out, err := exec.CommandContext(ctx, s.Bin, "--cg", "--box-id="+strconv.Itoa(id), "--init").Output()
	if err != nil {
		return nil, fmt.Errorf("isolate --init box %d: %w%s", id, err, stderrOf(err))
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return nil, fmt.Errorf("isolate --init box %d printed no path", id)
	}
	return &isolateBox{s: s, id: id, dir: filepath.Join(root, "box")}, nil
}

type isolateBox struct {
	s   *Isolate
	id  int
	dir string
}

func (b *isolateBox) Dir() string { return b.dir }

func (b *isolateBox) Close() error {
	out, err := exec.Command(b.s.Bin, "--cg", "--box-id="+strconv.Itoa(b.id), "--cleanup").CombinedOutput()
	if err != nil {
		return fmt.Errorf("isolate --cleanup box %d: %v: %s", b.id, err, bytes.TrimSpace(out))
	}
	return nil
}

func (b *isolateBox) Run(ctx context.Context, cmd Cmd) (Result, error) {
	meta := filepath.Join(b.s.MetaDir, fmt.Sprintf("box-%d.meta", b.id))
	_ = os.Remove(meta)
	args, err := isolateArgs(b.id, meta, cmd)
	if err != nil {
		return Result{}, err
	}
	c := exec.CommandContext(ctx, b.s.Bin, args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	runErr := c.Run()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	var exitErr *exec.ExitError
	if runErr != nil && (!errors.As(runErr, &exitErr) || exitErr.ExitCode() > 1) {
		return Result{}, fmt.Errorf("isolate --run: %v: %s", runErr, bytes.TrimSpace(stderr.Bytes()))
	}
	f, err := os.ReadFile(meta)
	if err != nil {
		return Result{}, fmt.Errorf("read isolate meta: %w", err)
	}
	return parseMeta(f)
}

func isolateArgs(boxID int, meta string, cmd Cmd) ([]string, error) {
	if len(cmd.Args) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	l := cmd.Limits
	secs := func(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }
	args := []string{
		"--cg",
		"--box-id=" + strconv.Itoa(boxID),
		"--meta=" + meta,
		"--time=" + secs(l.CPUTime),
		"--wall-time=" + secs(l.WallTime),
		"--extra-time=0.2",
		"--cg-mem=" + strconv.FormatInt(l.MemoryKiB, 10),
		"--processes=" + strconv.Itoa(max(l.Processes, 1)),
		"--fsize=" + strconv.FormatInt(l.FileSizeKiB, 10),
		"--open-files=" + strconv.Itoa(cmp0(l.OpenFiles, 64)),
	}
	if l.StackKiB > 0 {
		args = append(args, "--stack="+strconv.FormatInt(l.StackKiB, 10))
	}
	hasPath := false
	for _, e := range cmd.Env {
		args = append(args, "--env="+e)
		hasPath = hasPath || strings.HasPrefix(e, "PATH=")
	}
	if !hasPath {
		// isolate starts programs with an empty environment; compilers that look up their
		// helpers (as, ld, java for kotlinc) need a PATH.
		args = append(args, "--env=PATH="+defaultPath)
	}
	// isolate binds /dev recursively, which would carry the host's /dev/shm tmpfs into the box
	// writable. Give every box a private one instead (the write-outside escape case checks this).
	args = append(args, "--dir=/dev/shm:tmp")
	for _, d := range cmd.Dirs {
		args = append(args, "--dir="+d)
	}
	if cmd.Stdin != "" {
		args = append(args, "--stdin="+cmd.Stdin)
	}
	if cmd.Stdout != "" {
		args = append(args, "--stdout="+cmd.Stdout)
	}
	if cmd.StderrToStdout {
		args = append(args, "--stderr-to-stdout")
	} else if cmd.Stderr != "" {
		args = append(args, "--stderr="+cmd.Stderr)
	}
	prog, err := resolve(cmd.Args[0], cmd.Env)
	if err != nil {
		return nil, err
	}
	args = append(args, "--run", "--", prog)
	return append(args, cmd.Args[1:]...), nil
}

func resolve(prog string, env []string) (string, error) {
	if strings.Contains(prog, "/") {
		return prog, nil
	}
	path := defaultPath
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, prog)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("sandbox: %s not found in %s", prog, path)
}

func parseMeta(b []byte) (Result, error) {
	m := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), ":"); ok {
			m[k] = v
		}
	}
	num := func(k string) float64 {
		f, _ := strconv.ParseFloat(m[k], 64)
		return f
	}
	dur := func(k string) time.Duration { return time.Duration(math.Round(num(k) * float64(time.Second))) }
	r := Result{
		CPUTime:   dur("time"),
		WallTime:  dur("time-wall"),
		MemoryKiB: int64(max(num("cg-mem"), num("max-rss"))),
		ExitCode:  int(num("exitcode")),
		Signal:    int(num("exitsig")),
		Message:   m["message"],
	}
	switch m["status"] {
	case "":
		r.Status = OK
	case "RE":
		r.Status = NonZeroExit
	case "SG":
		r.Status = Signaled
		if r.Signal == 25 {
			r.Status = OutputLimit
		}
	case "TO":
		r.Status = TimedOut
	case "XX":
		return r, fmt.Errorf("isolate internal error: %s", r.Message)
	default:
		return r, fmt.Errorf("isolate: unknown status %q", m["status"])
	}
	if m["cg-oom-killed"] == "1" {
		r.Status = OutOfMemory
	}
	return r, nil
}

func cmp0(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func stderrOf(err error) string {
	var e *exec.ExitError
	if errors.As(err, &e) && len(e.Stderr) > 0 {
		return ": " + string(bytes.TrimSpace(e.Stderr))
	}
	return ""
}
