//go:build unix

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	RunHelperIfRequested()
	os.Exit(m.Run())
}

func newUnsafeBox(t *testing.T) Box {
	t.Helper()
	s, err := NewUnsafe(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Open(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

var shLimits = Limits{CPUTime: time.Second, WallTime: 3 * time.Second, MemoryKiB: 256 << 10, FileSizeKiB: 1024}

func TestUnsafeRun(t *testing.T) {
	b := newUnsafeBox(t)
	if err := os.WriteFile(filepath.Join(b.Dir(), "in"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := b.Run(context.Background(), Cmd{Args: []string{"cat"}, Env: []string{"PATH=/usr/bin:/bin"}, Stdin: "in", Stdout: "out", Limits: shLimits})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(b.Dir(), "out"))
	if r.Status != OK || string(out) != "hello\n" {
		t.Fatalf("result %+v output %q", r, out)
	}
	if runtime.GOOS == "linux" && r.MemoryKiB <= 0 {
		t.Fatalf("memory not measured: %+v", r)
	}
}

func TestUnsafeStatuses(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   Status
	}{
		{"exit code", "exit 3", NonZeroExit},
		{"signal", "kill -SEGV $$", Signaled},
		{"cpu time", "while :; do :; done", TimedOut},
		{"wall time", "sleep 10", TimedOut},
		{"file size", "exec head -c 5000000 /dev/zero", OutputLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newUnsafeBox(t)
			start := time.Now()
			r, err := b.Run(context.Background(), Cmd{Args: []string{"sh", "-c", tt.script}, Env: []string{"PATH=/usr/bin:/bin"}, Stdout: "out", Limits: shLimits})
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tt.want {
				t.Fatalf("status %v, want %v (%+v)", r.Status, tt.want, r)
			}
			if time.Since(start) > 6*time.Second {
				t.Fatalf("limits were not enforced in time: %v", time.Since(start))
			}
		})
	}
}
