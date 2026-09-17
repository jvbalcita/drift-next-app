package scrcpy

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// lineBuffer collects a child process's stderr so a run can report the server's
// own log when something goes wrong.
type lineBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lineBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lineBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.buf.String())
}

var _ io.Writer = (*lineBuffer)(nil)
