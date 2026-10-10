package ui

import (
	"strings"
	"testing"

	"codeberg.org/nathawat/lecturejet/internal/config"
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
	resolved, err := Configure(strings.NewReader("1\n1\ny\n4\n0\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Noise.Enabled || resolved.Noise.Strength != 0 || !resolved.Audio.Mono {
		t.Fatalf("unexpected RNNoise result: %+v", resolved)
	}
}

func TestInteractiveRNNoiseDefaultStrength(t *testing.T) {
	cfg := config.Default()
	resolved, err := Configure(strings.NewReader("\n\n\n\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Noise.Enabled || resolved.Noise.Strength != 100 || !resolved.Audio.Mono {
		t.Fatalf("unexpected RNNoise default: %+v", resolved.Noise)
	}
	if resolved.VideoCodec != config.VideoHEVC || resolved.HEVCCodecTag != config.HEVCCodecTagHVC1 {
		t.Fatalf("unexpected codec/tag defaults: codec=%v tag=%v", resolved.VideoCodec, resolved.HEVCCodecTag)
	}
}

func TestInteractiveRNNoiseExplicitStrengthChoices(t *testing.T) {
	cases := []struct {
		input    string
		strength int
	}{
		{"1\n1\ny\n1\n", 100},
		{"1\n1\ny\n2\n", 90},
		{"1\n1\ny\n3\n", 80},
		{"1\n1\ny\n4\n\n", 100},
		{"1\n1\ny\n4\n75\n", 75},
	}
	for _, tc := range cases {
		cfg := config.Default()
		resolved, err := Configure(strings.NewReader(tc.input), &strings.Builder{}, cfg,
			[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
		if err != nil {
			t.Fatalf("input %q failed: %v", tc.input, err)
		}
		if resolved.Noise.Strength != tc.strength {
			t.Fatalf("input %q: got strength %d, want %d", tc.input, resolved.Noise.Strength, tc.strength)
		}
	}
}

func TestInteractiveHEVCCodecTagChoice(t *testing.T) {
	cfg := config.Default()
	resolved, err := Configure(strings.NewReader("1\n1\nn\nn\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.HEVCCodecTag != config.HEVCCodecTagHVC1 {
		t.Fatalf("unexpected HEVC codec tag: %q", resolved.HEVCCodecTag)
	}

	resolvedHev1, err := Configure(strings.NewReader("1\n2\nn\nn\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatal(err)
	}
	if resolvedHev1.HEVCCodecTag != config.HEVCCodecTagHEV1 {
		t.Fatalf("unexpected HEVC codec tag: %q", resolvedHev1.HEVCCodecTag)
	}
}

func TestInteractiveRNNoiseEnforcesMonoWithoutPrompt(t *testing.T) {
	cfg := config.Default()
	cfg.Input = "in.mp4"
	cfg.Output = "out.mp4"
	resolved, err := Configure(strings.NewReader("1\n1\ny\n1\n"), &strings.Builder{}, cfg,
		[]VAAPIChoice{{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"}})
	if err != nil {
		t.Fatalf("unexpected wizard error: %v", err)
	}
	if !resolved.Noise.Enabled || !resolved.Audio.Mono {
		t.Fatalf("expected RNNoise enabled and mono output, got %+v", resolved)
	}
	if err := resolved.Validate(); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}
