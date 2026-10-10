package sandbox

import (
	"strings"
	"testing"
	"time"
)

func TestParseMeta(t *testing.T) {
	tests := []struct {
		name string
		meta string
		want Result
	}{
		{"ok", "time:0.012\ntime-wall:0.030\nmax-rss:3456\ncg-mem:4096\nexitcode:0\n", Result{Status: OK, CPUTime: 12 * time.Millisecond, WallTime: 30 * time.Millisecond, MemoryKiB: 4096}},
		{"runtime error", "status:RE\nexitcode:3\ntime:0.001\nmessage:Exited with error status 3\n", Result{Status: NonZeroExit, ExitCode: 3, CPUTime: time.Millisecond, Message: "Exited with error status 3"}},
		{"segfault", "status:SG\nexitsig:11\n", Result{Status: Signaled, Signal: 11}},
		{"file size", "status:SG\nexitsig:25\n", Result{Status: OutputLimit, Signal: 25}},
		{"timeout", "status:TO\ntime:1.204\nkilled:1\nmessage:Time limit exceeded\n", Result{Status: TimedOut, CPUTime: 1204 * time.Millisecond, Message: "Time limit exceeded"}},
		{"oom", "status:SG\nexitsig:9\ncg-oom-killed:1\ncg-mem:262144\n", Result{Status: OutOfMemory, Signal: 9, MemoryKiB: 262144}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMeta([]byte(tt.meta))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
	if _, err := parseMeta([]byte("status:XX\nmessage:box broken\n")); err == nil || !strings.Contains(err.Error(), "box broken") {
		t.Fatalf("XX should be an error, got %v", err)
	}
}

func TestIsolateArgs(t *testing.T) {
	args, err := isolateArgs(3, "/tmp/m", Cmd{
		Args:   []string{"./main", "x"},
		Env:    []string{"PATH=/usr/bin:/bin"},
		Stdin:  "input.txt",
		Stdout: "output.txt",
		Stderr: "stderr.txt",
		Dirs:   []string{"/etc"},
		Limits: Limits{CPUTime: 1500 * time.Millisecond, WallTime: 4 * time.Second, MemoryKiB: 262144, StackKiB: 262144, FileSizeKiB: 65536, Processes: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := "--cg --box-id=3 --meta=/tmp/m --time=1.500 --wall-time=4.000 --extra-time=0.2 --cg-mem=262144 --processes=1 --fsize=65536 --open-files=64 --stack=262144 --env=PATH=/usr/bin:/bin --dir=/etc --stdin=input.txt --stdout=output.txt --stderr=stderr.txt --run -- ./main x"
	if got != want {
		t.Fatalf("args:\n got %s\nwant %s", got, want)
	}
	args, err = isolateArgs(0, "/tmp/m", Cmd{Args: []string{"sh", "-c", "true"}, StderrToStdout: true, Stdout: "log"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "--env=PATH=/usr/local/bin:/usr/bin:/bin --stdout=log --stderr-to-stdout --run -- /") || !strings.HasSuffix(got, "/sh -c true") {
		t.Fatalf("args %s", got)
	}
	if _, err := isolateArgs(0, "/tmp/m", Cmd{Args: []string{"no-such-binary-xyz"}}); err == nil {
		t.Fatal("expected a lookup error")
	}
}
