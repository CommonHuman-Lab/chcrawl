package benchlab

import (
	"os/exec"
	"syscall"
)

func childPeakRSSBytes(cmd *exec.Cmd) (uint64, bool) {
	if cmd.ProcessState == nil {
		return 0, false
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil || ru.Maxrss <= 0 {
		return 0, false
	}
	return uint64(ru.Maxrss) * 1024, true
}
