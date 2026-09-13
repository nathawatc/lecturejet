//go:build cgo && linux

package media

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidRNNoiseConfigurationFailsClearly(t *testing.T) {
	if _, err := OpenAudio("not-used", 1, true, 0.2, ""); err == nil {
		t.Fatal("missing RNNoise model accepted")
	}
}

func TestRemuxCancellationReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RemuxHEVCHVC1WithContext(ctx, "not-used", filepath.Join(t.TempDir(), "output.mp4")); !errors.Is(err, context.Canceled) {
		t.Fatalf("remux error=%v, want context cancellation", err)
	}
}

func TestRemuxRejectsNonHEVCFixture(t *testing.T) {
	path := os.Getenv("LECTUREJET_TEST_MEDIA")
	if path == "" {
		t.Skip("set LECTUREJET_TEST_MEDIA to run remux bridge test")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(info.VideoCodec, "hevc") {
		t.Skip("fixture contains HEVC")
	}
	err = RemuxHEVCHVC1WithContext(context.Background(), path,
		filepath.Join(t.TempDir(), "output.mp4"))
	if err == nil || !strings.Contains(err.Error(), "no HEVC video stream") {
		t.Fatalf("remux error=%v, want missing HEVC error", err)
	}
}

func TestRemuxHEVCFixturePreservesStreams(t *testing.T) {
	path := os.Getenv("LECTUREJET_TEST_HEVC_MEDIA")
	if path == "" {
		t.Skip("set LECTUREJET_TEST_HEVC_MEDIA to run HEVC remux test")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Video || !strings.Contains(before.VideoCodec, "hevc") {
		t.Fatalf("fixture is not HEVC: %+v", before)
	}
	output := filepath.Join(t.TempDir(), "output.mp4")
	if err := RemuxHEVCHVC1WithContext(context.Background(), path, output); err != nil {
		t.Fatal(err)
	}
	after, err := Probe(output)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Video || !after.Audio || !strings.Contains(after.VideoCodec, "hevc") {
		t.Fatalf("remux lost streams: before=%+v after=%+v", before, after)
	}
	delta := after.DurationNS - before.DurationNS
	if delta < 0 {
		delta = -delta
	}
	if delta > 100_000_000 {
		t.Fatalf("remux duration delta=%d ns", delta)
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
	decoder, err := OpenAudio(path, 1, false, 0, "")
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
	decoder, err := OpenAudio(path, 1, true, 0.2, model)
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
	for _, codec := range []string{"h264_vaapi", "hevc_vaapi"} {
		if err := ProbeVAAPI(device, codec, info.VideoWidth, info.VideoHeight); err != nil {
			t.Skipf("%s unavailable on %s: %v", codec, device, err)
		}
	}
}
