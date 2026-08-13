package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lecturejet/internal/config"
	"lecturejet/internal/media"
	"lecturejet/internal/timeline"
)

func TestAnalyzeCancellationWaitsForDecoder(t *testing.T) {
	path := os.Getenv("LECTUREJET_TEST_MEDIA")
	if path == "" {
		t.Skip("set LECTUREJET_TEST_MEDIA to run cancellation integration test")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Input = path
	cfg.Output = filepath.Join(t.TempDir(), "unused.mp4")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Analyze(ctx, cfg, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Analyze error=%v, want context cancellation", err)
	}
}

type cancelOnRenderProgress struct {
	cancel context.CancelFunc
}

func (w cancelOnRenderProgress) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(" 5%")) {
		w.cancel()
	}
	return len(p), nil
}

func TestRenderCancellationRemovesTemporaryOutput(t *testing.T) {
	path := os.Getenv("LECTUREJET_TEST_RENDER_MEDIA")
	device := os.Getenv("LECTUREJET_VAAPI_DEVICE")
	if path == "" || device == "" {
		t.Skip("set LECTUREJET_TEST_RENDER_MEDIA and LECTUREJET_VAAPI_DEVICE to run render cancellation test")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := media.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	resultTimeline, err := timeline.FromCuts(info.DurationNS, nil)
	if err != nil {
		t.Fatal(err)
	}
	outputDirectory := t.TempDir()
	output := filepath.Join(outputDirectory, "cancelled.mp4")
	cfg := config.Default()
	cfg.Input = path
	cfg.Output = output
	cfg.VAAPIDevice = device
	cfg.VideoCodec = config.VideoH264
	cfg.Audio.Mono = true
	ctx, cancel := context.WithCancel(context.Background())
	err = Render(ctx, cfg, Result{Timeline: resultTimeline, VAAPIDevice: device,
		VideoCodec: config.VideoH264}, cancelOnRenderProgress{cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Render error=%v, want context cancellation", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled output stat error=%v, output should not be published", err)
	}
	temporary, err := filepath.Glob(filepath.Join(outputDirectory, ".cancelled.mp4.lecturejet-*.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary output remains: %v", temporary)
	}
}
