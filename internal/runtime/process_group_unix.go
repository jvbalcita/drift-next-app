//go:build !windows

package runtime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := syscall.Kill(-process.Pid, syscall.SIGTERM); err == nil {
		return nil
	}
	return process.Kill()
}

// terminateProcess asks one process to stop, for the case where the operator
// explicitly chose to terminate a listener this session did not start.
//
// It signals the process itself, never its group: a process this session did not
// start is not ours to take a group down with, and the group may contain
// processes the operator started deliberately for something else.
//
// It stops at asking. Nothing here escalates to SIGKILL: a process that ignores
// the request is the operator's decision to make, and the caller reports the
// outcome instead of forcing it.
func terminateProcess(pid int) error {
	if err := validateTerminable(pid); err != nil {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		// A process that has already exited is not a failed termination: the
		// caller decides on the address, not on the signal.
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("signal process %d: %w", pid, err)
	}
	return nil
}
