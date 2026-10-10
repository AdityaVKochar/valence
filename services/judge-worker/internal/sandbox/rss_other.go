//go:build unix && !linux && !darwin

package sandbox

import "syscall"

func maxRSSKiB(ru *syscall.Rusage) int64 { return int64(ru.Maxrss) }
