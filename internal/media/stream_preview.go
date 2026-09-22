package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

// PreviewFrameDecoder turns one self-contained H.264 key frame into a PNG. The
// implementation is shared and bounded by StreamPreviewCapturer; it is never
// invoked concurrently beyond DecodeWorkers.
type PreviewFrameDecoder interface {
	DecodePNG(ctx context.Context, annexB []byte) ([]byte, error)
}

// StreamPreviewCapturerConfig configures the persistent grid capture path.
type StreamPreviewCapturerConfig struct {
	Dialer        MirrorDialer
	Decoder       PreviewFrameDecoder
	MaxWorkers    int
	StartWorkers  int
	DecodeWorkers int
	Preview       MirrorPreview
	CloseTimeout  time.Duration
}

// StreamPreviewCapturer adapts persistent scrcpy streams to the still capture
// contract. Each device has one reader that continuously drains its video
// socket and retains exactly one latest key frame. Screenshot samples that
// latest value through a shared bounded decoder; it never queues frame history.
type StreamPreviewCapturer struct {
	dialer       MirrorDialer
	decoder      PreviewFrameDecoder
	preview      MirrorPreview
	starts       chan struct{}
	decodes      chan struct{}
	maxWorkers   int
	closeTimeout time.Duration

	mu      sync.Mutex
	workers map[string]*streamPreviewWorker
}

type streamPreviewWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}

	mu       sync.RWMutex
	frame    []byte
	captured time.Time
	err      error
}

func NewStreamPreviewCapturer(config StreamPreviewCapturerConfig) (*StreamPreviewCapturer, error) {
	if config.Dialer == nil || config.Decoder == nil {
		return nil, errors.New("media: persistent grid previews require a scrcpy dialer and frame decoder")
	}
	if config.MaxWorkers <= 0 {
		config.MaxWorkers = 25
	}
	if config.StartWorkers <= 0 {
		config.StartWorkers = min(2, config.MaxWorkers)
	}
	if config.DecodeWorkers <= 0 {
		config.DecodeWorkers = min(2, config.MaxWorkers)
	}
	if config.StartWorkers > config.MaxWorkers || config.DecodeWorkers > config.MaxWorkers {
		return nil, errors.New("media: preview start and decode concurrency may not exceed the worker bound")
	}
	if config.Preview.Quality == "" {
		config.Preview = MirrorPreview{Quality: PreviewLow, FrameRate: 1}
	}
	if config.Preview.FrameRate != 1 {
		return nil, fmt.Errorf("media: persistent grid capture requires the bounded 1 fps scrcpy profile, got %d", config.Preview.FrameRate)
	}
	if config.CloseTimeout <= 0 {
		config.CloseTimeout = 5 * time.Second
	}
	return &StreamPreviewCapturer{
		dialer: config.Dialer, decoder: config.Decoder, preview: config.Preview,
		starts: make(chan struct{}, config.StartWorkers), decodes: make(chan struct{}, config.DecodeWorkers),
		maxWorkers: config.MaxWorkers, closeTimeout: config.CloseTimeout,
		workers: make(map[string]*streamPreviewWorker),
	}, nil
}

func (c *StreamPreviewCapturer) Screenshot(ctx context.Context, serial string) (adb.ScreenshotResult, error) {
	if err := adb.ValidateSerial(serial); err != nil {
		return adb.ScreenshotResult{}, err
	}
	worker, err := c.worker(serial)
	if err != nil {
		return adb.ScreenshotResult{}, err
	}
	select {
	case <-ctx.Done():
		return adb.ScreenshotResult{}, ctx.Err()
	case <-worker.ready:
	}
	worker.mu.RLock()
	encoded := append([]byte(nil), worker.frame...)
	captured := worker.captured
	workerErr := worker.err
	worker.mu.RUnlock()
	if workerErr != nil {
		return adb.ScreenshotResult{}, workerErr
	}
	if len(encoded) == 0 {
		return adb.ScreenshotResult{}, errors.New("the scrcpy preview ended before producing a key frame")
	}
	select {
	case c.decodes <- struct{}{}:
		defer func() { <-c.decodes }()
	case <-ctx.Done():
		return adb.ScreenshotResult{}, ctx.Err()
	}
	started := time.Now()
	png, err := c.decoder.DecodePNG(ctx, encoded)
	if err != nil {
		return adb.ScreenshotResult{}, fmt.Errorf("decode persistent preview for %s: %w", serial, err)
	}
	hash := sha256.Sum256(png)
	return adb.ScreenshotResult{Serial: serial, CapturedAt: captured, PNG: png, Hash: hex.EncodeToString(hash[:]), Latency: time.Since(started)}, nil
}

func (c *StreamPreviewCapturer) worker(serial string) (*streamPreviewWorker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if worker := c.workers[serial]; worker != nil {
		return worker, nil
	}
	if len(c.workers) >= c.maxWorkers {
		return nil, fmt.Errorf("media: persistent grid capture carries at most %d device workers", c.maxWorkers)
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &streamPreviewWorker{cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
	c.workers[serial] = worker
	go c.runWorker(ctx, serial, worker)
	return worker, nil
}

func (c *StreamPreviewCapturer) runWorker(ctx context.Context, serial string, worker *streamPreviewWorker) {
	defer close(worker.done)
	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		select {
		case c.starts <- struct{}{}:
		case <-ctx.Done():
			worker.finish(ctx.Err())
			return
		}
		stream, err := c.dialer.Dial(ctx, serial, serial, PurposeAmbient, c.preview)
		<-c.starts
		if err == nil {
			err = c.readStream(ctx, worker, stream)
		}
		worker.finish(err)
		if ctx.Err() != nil {
			return
		}
		jitterBound := max(backoff/4, time.Nanosecond)
		jitter := time.Duration(time.Now().UnixNano() % int64(jitterBound))
		timer := time.NewTimer(backoff + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func (c *StreamPreviewCapturer) readStream(ctx context.Context, worker *streamPreviewWorker, stream MirrorStream) error {
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), c.closeTimeout)
		defer cancel()
		_ = stream.Close(closeCtx)
	}()
	for {
		frame, readErr := stream.ReadFrame(ctx)
		if readErr != nil {
			return readErr
		}
		if frame.Declared || !frame.Key || len(frame.Data) == 0 {
			continue
		}
		worker.store(frame.Data, time.Now().UTC())
	}
}

func (w *streamPreviewWorker) store(frame []byte, captured time.Time) {
	w.mu.Lock()
	first := len(w.frame) == 0 && w.err == nil
	w.frame = append(w.frame[:0], frame...)
	w.captured = captured
	w.err = nil
	w.mu.Unlock()
	if first {
		close(w.ready)
	}
}

func (w *streamPreviewWorker) finish(err error) {
	w.mu.Lock()
	first := len(w.frame) == 0 && w.err == nil
	w.err = err
	w.mu.Unlock()
	if first {
		close(w.ready)
	}
}

// Release stops and forgets one device worker. FrameEngine calls it when the
// grid releases that device, so no encoder survives its subscription.
func (c *StreamPreviewCapturer) Release(serial string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	worker := c.workers[serial]
	delete(c.workers, serial)
	c.mu.Unlock()
	if worker != nil {
		worker.cancel()
		<-worker.done
	}
}

func (c *StreamPreviewCapturer) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	serials := make([]string, 0, len(c.workers))
	for serial := range c.workers {
		serials = append(serials, serial)
	}
	c.mu.Unlock()
	for _, serial := range serials {
		c.Release(serial)
	}
}
