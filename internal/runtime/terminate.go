package runtime

import (
	"errors"
	"fmt"
	"os"
)

// validateTerminable refuses a pid this runtime must never signal, whatever the
// operator chose. Terminating a foreign listener is the one place this runtime
// signals a process it did not start, so the guard is deliberately strict:
//
//   - pid 0 means "every process in the caller's group" to kill(2), and a
//     negative pid means a whole process group, either of which could take down
//     far more than the listener the operator meant;
//   - pid 1 is the init process, which on a container host is the machine;
//   - this session's own pid would end the console mid-operation, leaving the
//     terminal and every owned child behind.
func validateTerminable(pid int) error {
	switch {
	case pid <= 1:
		return fmt.Errorf("refusing to signal pid %d: it is not a listener this session can terminate", pid)
	case pid == os.Getpid():
		return errors.New("refusing to signal this session's own process")
	default:
		return nil
	}
}
