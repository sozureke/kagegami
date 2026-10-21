package terminal

import "syscall"

func umask(mask int) int { return syscall.Umask(mask) }
