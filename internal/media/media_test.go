//go:build cgo && linux

package media

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/timeline"
)

func TestInvalidRNNoiseConfigurationFailsClearly(t *testing.T) {
	if _, err := OpenAudio(context.Background(), "not-used", 1, true, 0.2, ""); err == nil {
		t.Fatal("missing RNNoise model accepted")
	}
}

func TestRenderRejectsEmptyTimeline(t *testing.T) {
	err := Render(context.Background(), "not-used.mp4", "output.mp4", timeline.Timeline{}, RenderOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no segments") {
		t.Fatalf("expected error for empty timeline, got: %v", err)
	}
}

func TestFixtureProbeAndFiniteAudio(t *testing.T) {
	path := os.Getenv("LECTUREJET_TEST_MEDIA")
	if path == "" {
		t.Skip("set LECTUREJET_TEST_MEDIA to run media fixture tests")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.DurationNS <= 0 || !info.Audio || !info.Video {
		t.Fatalf("incomplete probe: %+v", info)
	}
	decoder, err := OpenAudio(context.Background(), path, 1, false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	buffer := make([]float32, 8192)
	var total int64
	for {
		count, start, err := decoder.Read(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		if start != total {
			t.Fatalf("audio sample cursor=%d, want %d", start, total)
		}
		for _, sample := range buffer[:count] {
			if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
				t.Fatal("non-finite decoded PCM")
			}
		}
		total += int64(count)
	}
	if total == 0 {
		t.Fatal("fixture decoded no audio samples")
	}
}

func TestFixtureRNNoiseFiniteAudio(t *testing.T) {
	path := os.Getenv("LECTUREJET_TEST_MEDIA")
	model := os.Getenv("LECTUREJET_TEST_RNNOISE_MODEL")
	if path == "" || model == "" {
		t.Skip("set LECTUREJET_TEST_MEDIA and LECTUREJET_TEST_RNNOISE_MODEL to run RNNoise fixture test")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := OpenAudio(context.Background(), path, 1, true, 0.2, model)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	buffer := make([]float32, 8192)
	var total int64
	for {
		count, start, err := decoder.Read(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		if start != total {
			t.Fatalf("RNNoise audio sample cursor=%d, want %d", start, total)
		}
		for _, sample := range buffer[:count] {
			if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
				t.Fatal("non-finite RNNoise PCM")
			}
		}
		total += int64(count)
	}
	if total == 0 {
		t.Fatal("RNNoise fixture decoded no audio samples")
	}
}

func TestFixtureVAAPIProbe(t *testing.T) {
	path := os.Getenv("LECTUREJET_TEST_MEDIA")
	device := os.Getenv("LECTUREJET_VAAPI_DEVICE")
	if path == "" || device == "" {
		t.Skip("set LECTUREJET_TEST_MEDIA and LECTUREJET_VAAPI_DEVICE to run VAAPI probe")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{string(config.VideoH264), string(config.VideoHEVC)} {
		if err := ProbeVAAPI(device, codec, info.VideoWidth, info.VideoHeight); err != nil {
			t.Skipf("%s unavailable on %s: %v", codec, device, err)
		}
	}
}
