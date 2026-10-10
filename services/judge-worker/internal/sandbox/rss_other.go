//go:build unix && !linux && !darwin

package sandbox

import "syscall"

func maxRSSKiB(ru *syscall.Rusage) int64 { return int64(ru.Maxrss) }

func currentRSSKiB(int) (int64, bool) { return 0, false }
