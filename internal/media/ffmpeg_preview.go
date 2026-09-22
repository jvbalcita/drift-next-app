package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const maxDecodedPreviewBytes = 4 << 20

// FFmpegPreviewDecoder decodes one self-contained H.264 key frame. Processes
// are short-lived and their concurrency is owned by StreamPreviewCapturer's
// shared semaphore, so the fleet cannot create an unrestricted decoder set.
type FFmpegPreviewDecoder struct{ path string }

func NewFFmpegPreviewDecoder(path string) (*FFmpegPreviewDecoder, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("media: the grid ffmpeg path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("media: the grid ffmpeg path is not readable: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("media: the grid ffmpeg path names a directory")
	}
	return &FFmpegPreviewDecoder{path: path}, nil
}

func (d *FFmpegPreviewDecoder) DecodePNG(ctx context.Context, annexB []byte) ([]byte, error) {
	if d == nil || d.path == "" || len(annexB) == 0 {
		return nil, errors.New("media: decoding a grid preview requires ffmpeg and an H.264 key frame")
	}
	cmd := exec.CommandContext(ctx, d.path,
		"-hide_banner", "-loglevel", "error", "-f", "h264", "-i", "pipe:0",
		"-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "pipe:1")
	cmd.Stdin = bytes.NewReader(annexB)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr boundedPreviewLog
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	png, readErr := io.ReadAll(io.LimitReader(stdout, maxDecodedPreviewBytes+1))
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if len(png) > maxDecodedPreviewBytes {
		return nil, fmt.Errorf("media: decoded preview exceeds %d bytes", maxDecodedPreviewBytes)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("media: ffmpeg could not decode the preview: %s", stderr.String())
	}
	if len(png) == 0 {
		return nil, errors.New("media: ffmpeg decoded no preview image")
	}
	return png, nil
}

type boundedPreviewLog struct{ bytes.Buffer }

func (b *boundedPreviewLog) Write(p []byte) (int, error) {
	const limit = 2048
	if b.Len() < limit {
		_, _ = b.Buffer.Write(p[:min(len(p), limit-b.Len())])
	}
	return len(p), nil
}
