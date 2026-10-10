package sandbox

import "syscall"

func maxRSSKiB(ru *syscall.Rusage) int64 { return ru.Maxrss / 1024 }

func currentRSSKiB(int) (int64, bool) { return 0, false }
