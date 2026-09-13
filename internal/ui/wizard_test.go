package ui

import (
	"strings"
	"testing"

	"lecturejet/internal/config"
)

func TestInteractiveChoicesResolveExpectedConfig(t *testing.T) {
	cfg := config.Default()
	resolved, err := Configure(strings.NewReader("2\nn\nn\n"), &strings.Builder{}, cfg, []VAAPIChoice{
		{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"},
		{Codec: config.VideoH264, Device: "/dev/dri/renderD129"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.VideoCodec != config.VideoH264 || resolved.VAAPIDevice != "/dev/dri/renderD129" ||
		resolved.Noise.Enabled || resolved.Audio.Mono {
		t.Fatalf("unexpected wizard result: %+v", resolved)
	}
}

func TestInteractiveRNNoiseStrengthChoice(t *testing.T) {
	cfg := config.Default()
	resolved, err := Configure(strings.NewReader("1\n1\ny\n4\n0\nmodel.rnnn\n\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Noise.Enabled || resolved.Noise.Strength != 0 || resolved.Noise.Model != "model.rnnn" || !resolved.Audio.Mono {
		t.Fatalf("unexpected RNNoise result: %+v", resolved)
	}
}

func TestInteractiveRNNoiseDefaultStrength(t *testing.T) {
	cfg := config.Default()
	resolved, err := Configure(strings.NewReader("1\n1\ny\n\nmodel.rnnn\n\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Noise.Enabled || resolved.Noise.Strength != 80 || resolved.Noise.Model != "model.rnnn" {
		t.Fatalf("unexpected RNNoise default: %+v", resolved.Noise)
	}
}

func TestInteractiveHEVCCodecTagChoice(t *testing.T) {
	cfg := config.Default()
	resolved, err := Configure(strings.NewReader("1\n2\nn\nn\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.HEVCCodecTag != config.HEVCCodecTagHVC1 {
		t.Fatalf("unexpected HEVC codec tag: %q", resolved.HEVCCodecTag)
	}
}

func TestInteractiveRNNoiseWithStereoYieldsInvalidConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Input = "in.mp4"
	cfg.Output = "out.mp4"
	resolved, err := Configure(strings.NewReader("1\n1\ny\n\nmodel.rnnn\nn\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatalf("unexpected wizard error: %v", err)
	}
	if !resolved.Noise.Enabled || resolved.Audio.Mono {
		t.Fatalf("expected RNNoise enabled and stereo output, got %+v", resolved)
	}
	if err := resolved.Validate(); err == nil {
		t.Fatal("expected invalid config for RNNoise + stereo, got valid config")
	}
}
