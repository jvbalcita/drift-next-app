package media_test

import (
	"bytes"
	"context"
	"image/png"
	"os/exec"
	"testing"
	"time"

	"drift.local/drift-next/internal/media"
)

func TestFFmpegPreviewDecoderDecodesOneBoundedKeyFrame(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed on this test host")
	}
	encoded, err := exec.Command(path,
		"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=360x760:r=1",
		"-frames:v", "1", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-f", "h264", "pipe:1").Output()
	if err != nil {
		t.Fatalf("generate H.264 fixture: %v", err)
	}
	decoder, err := media.NewFFmpegPreviewDecoder(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	decoded, err := decoder.DecodePNG(ctx, encoded)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decoded output is not PNG: %v", err)
	}
	if image.Width != 360 || image.Height != 760 {
		t.Fatalf("decoded size = %dx%d, want 360x760", image.Width, image.Height)
	}
}
