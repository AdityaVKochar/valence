package sandbox

import "syscall"

func maxRSSKiB(ru *syscall.Rusage) int64 { return ru.Maxrss / 1024 }
