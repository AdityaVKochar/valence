package sandbox

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
)

func maxRSSKiB(ru *syscall.Rusage) int64 { return ru.Maxrss }

// currentRSSKiB reads the resident set size of a running process from /proc.
func currentRSSKiB(pid int) (int64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0, false
	}
	f := bytes.Fields(b)
	if len(f) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseInt(string(f[1]), 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * int64(os.Getpagesize()) / 1024, true
}
