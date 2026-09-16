//go:build darwin

package main

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// Darwin exposes terminal attributes through TIOCGETA/TIOCSETA.
const (
	ioctlReadTermios  = syscall.TIOCGETA
	ioctlWriteTermios = syscall.TIOCSETA
)

// resizeSignal is delivered when the operator resizes the window.
func resizeSignal() os.Signal { return syscall.SIGWINCH }

// terminalSize reads the window size, or (0, 0) when it is unknown.
func terminalSize(file *os.File) (int, int) {
	if file == nil {
		return 0, 0
	}
	var size struct{ rows, columns, xpixel, ypixel uint16 }
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, file.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)), 0, 0, 0); errno != 0 {
		return 0, 0
	}
	return int(size.columns), int(size.rows)
}

// enableRaw puts the input stream into cbreak mode and returns the function
// that restores the previous attributes exactly once.
func enableRaw(file *os.File) (func(), error) {
	if file == nil {
		return nil, syscall.EBADF
	}
	descriptor := file.Fd()
	var original syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, descriptor, ioctlReadTermios, uintptr(unsafe.Pointer(&original)), 0, 0, 0); errno != 0 {
		return nil, errno
	}
	raw := original
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, descriptor, ioctlWriteTermios, uintptr(unsafe.Pointer(&raw)), 0, 0, 0); errno != 0 {
		return nil, errno
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_, _, _ = syscall.Syscall6(syscall.SYS_IOCTL, descriptor, ioctlWriteTermios, uintptr(unsafe.Pointer(&original)), 0, 0, 0)
		})
	}, nil
}
