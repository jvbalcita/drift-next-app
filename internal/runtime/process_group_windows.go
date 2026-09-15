//go:build windows

package runtime

import (
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
