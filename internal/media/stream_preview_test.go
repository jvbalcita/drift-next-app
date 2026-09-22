package media_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"drift.local/drift-next/internal/media"
)

func TestStreamPreviewCapturerRetainsOnlyTheLatestKeyFrame(t *testing.T) {
	stream := newPreviewStream()
	dialer := &previewDialer{stream: stream}
	capturer, err := media.NewStreamPreviewCapturer(media.StreamPreviewCapturerConfig{
		Dialer: dialer, Decoder: &previewDecoder{}, MaxWorkers: 1, StartWorkers: 1, DecodeWorkers: 1,
		Preview: media.MirrorPreview{Quality: media.PreviewLow, FrameRate: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer capturer.Close()

	ready := make(chan error, 1)
	go func() {
		_, captureErr := capturer.Screenshot(context.Background(), "SERIAL-A")
		ready <- captureErr
	}()
	stream.frames <- media.StreamFrame{Key: true, Data: []byte("old")}
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	stream.frames <- media.StreamFrame{Data: []byte("not-a-key-frame")}
	stream.frames <- media.StreamFrame{Key: true, Data: []byte("latest")}
	time.Sleep(10 * time.Millisecond)
	shot, err := capturer.Screenshot(context.Background(), "SERIAL-A")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(shot.PNG); got != "latest" {
		t.Fatalf("decoded %q, want only the latest retained key frame", got)
	}
	if dialer.calls != 1 {
		t.Fatalf("dialed %d times, want one persistent worker", dialer.calls)
	}
}

func TestStreamPreviewCapturerReleaseClosesThePersistentStream(t *testing.T) {
	stream := newPreviewStream()
	capturer, err := media.NewStreamPreviewCapturer(media.StreamPreviewCapturerConfig{
		Dialer: &previewDialer{stream: stream}, Decoder: &previewDecoder{}, MaxWorkers: 1,
		Preview: media.MirrorPreview{Quality: media.PreviewLow, FrameRate: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		_, captureErr := capturer.Screenshot(context.Background(), "SERIAL-A")
		ready <- captureErr
	}()
	stream.frames <- media.StreamFrame{Key: true, Data: []byte("frame")}
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	capturer.Release("SERIAL-A")
	select {
	case <-stream.closed:
	case <-time.After(time.Second):
		t.Fatal("release did not close the device stream")
	}
}

func TestStreamPreviewCapturerRecoversOneDeviceIndependently(t *testing.T) {
	stream := newPreviewStream()
	dialer := &recoveringPreviewDialer{stream: stream}
	capturer, err := media.NewStreamPreviewCapturer(media.StreamPreviewCapturerConfig{
		Dialer: dialer, Decoder: &previewDecoder{}, MaxWorkers: 1,
		Preview: media.MirrorPreview{Quality: media.PreviewLow, FrameRate: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer capturer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := capturer.Screenshot(ctx, "SERIAL-A"); err == nil {
		t.Fatal("the failed first dial was reported as a frame")
	}
	stream.frames <- media.StreamFrame{Key: true, Data: []byte("recovered")}
	for {
		shot, captureErr := capturer.Screenshot(ctx, "SERIAL-A")
		if captureErr == nil {
			if string(shot.PNG) != "recovered" {
				t.Fatalf("recovered PNG = %q", shot.PNG)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("worker did not recover: %v", captureErr)
		case <-time.After(25 * time.Millisecond):
		}
	}
	if dialer.calls < 2 {
		t.Fatalf("dialed %d time(s), want an independent retry", dialer.calls)
	}
}

type previewDialer struct {
	mu     sync.Mutex
	calls  int
	stream *previewStream
}

func (d *previewDialer) Dial(context.Context, string, string, media.MirrorViewerPurpose, media.MirrorPreview) (media.MirrorStream, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return d.stream, nil
}

type previewDecoder struct{}

func (*previewDecoder) DecodePNG(_ context.Context, frame []byte) ([]byte, error) {
	return append([]byte(nil), frame...), nil
}

type recoveringPreviewDialer struct {
	mu     sync.Mutex
	calls  int
	stream *previewStream
}

func (d *recoveringPreviewDialer) Dial(context.Context, string, string, media.MirrorViewerPurpose, media.MirrorPreview) (media.MirrorStream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.calls == 1 {
		return nil, errors.New("temporary dial failure")
	}
	return d.stream, nil
}

type previewStream struct {
	frames chan media.StreamFrame
	closed chan struct{}
	once   sync.Once
}

func newPreviewStream() *previewStream {
	return &previewStream{frames: make(chan media.StreamFrame, 8), closed: make(chan struct{})}
}

func (*previewStream) FrameSize(context.Context) (int, int, error) { return 360, 760, nil }
func (s *previewStream) ReadFrame(ctx context.Context) (media.StreamFrame, error) {
	select {
	case <-ctx.Done():
		return media.StreamFrame{}, ctx.Err()
	case frame := <-s.frames:
		return frame, nil
	}
}
func (*previewStream) SendInput(context.Context, media.MirrorInput) error {
	return errors.New("grid previews do not send input")
}
func (*previewStream) RequestKeyframe(context.Context) error { return nil }
func (s *previewStream) Close(context.Context) error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
