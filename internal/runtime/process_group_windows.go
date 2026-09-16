//go:build windows

package runtime

import (
	"fmt"
	"os"
	"os/exec"
)

func configureProcessGroup(_ *exec.Cmd) {}

func stopProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

// terminateProcess asks one process to stop, for the case where the operator
// explicitly chose to terminate a listener this session did not start. The
// platform offers no graceful equivalent of SIGTERM here, so this is the same
// kill the session already uses for its own children, applied to one pid.
func terminateProcess(pid int) error {
	if err := validateTerminable(pid); err != nil {
		return err
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := process.Kill(); err != nil {
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}
	return nil
}
