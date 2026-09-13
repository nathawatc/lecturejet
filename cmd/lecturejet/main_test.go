package main

import (
	"strings"
	"testing"

	"lecturejet/internal/config"
	"lecturejet/internal/ui"
)

func TestParseRenderFlags(t *testing.T) {
	cfg, interactive, explicit, err := parseRenderArgs([]string{
		"input.mp4", "output.mp4", "--video-codec", "hevc", "--hevc-tag", "hvc1",
		"--stereo", "--threads", "auto", "--non-interactive",
	})
	if err != nil {
		t.Fatal(err)
	}
	if interactive || !explicit || cfg.VideoCodec != config.VideoHEVC ||
		cfg.HEVCCodecTag != config.HEVCCodecTagHVC1 || cfg.Audio.Mono || cfg.Threads != 0 {
		t.Fatalf("unexpected parsed config: %+v interactive=%v explicit=%v", cfg, interactive, explicit)
	}
}

func TestParseRejectsConflictingAndInvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"input.mp4", "output.mp4", "--mono", "--stereo"},
		{"input.mp4", "output.mp4", "--threads", "0"},
		{"input.mp4", "output.mp4", "--hysteresis", "4"},
	} {
		if _, _, _, err := parseRenderArgs(args); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
}

func TestParseAnalyzeRejectsObsoleteHysteresisFlag(t *testing.T) {
	if _, err := parseAnalyzeArgs([]string{"input.mp4", "--hysteresis", "4"}); err == nil {
		t.Fatal("accepted obsolete --hysteresis flag in analyze")
	}
}

func TestParseRNNoiseStereoReachesValidationAndIsRejected(t *testing.T) {
	cfg, _, _, err := parseRenderArgs([]string{
		"input.mp4", "output.mp4",
		"--rnnoise",
		"--rnnoise-model", "model.rnnn",
		"--stereo",
		"--non-interactive",
	})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !cfg.Noise.Enabled || cfg.Audio.Mono {
		t.Fatalf("expected rnnoise enabled and stereo output, got %+v", cfg)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected RNNoise + stereo configuration to be rejected by validation")
	}
}

func TestSelectVAAPIConfig(t *testing.T) {
	t.Run("case 1: implicit device + implicit codec chooses preferred HEVC", func(t *testing.T) {
		cfg := config.Default()
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"},
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		resolved, err := selectVAAPIConfig(cfg, choices, false, false, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoHEVC || resolved.VAAPIDevice != "/dev/dri/renderD128" {
			t.Fatalf("expected HEVC on /dev/dri/renderD128, got codec=%s device=%s", resolved.VideoCodec, resolved.VAAPIDevice)
		}
	})

	t.Run("case 2: explicit device + implicit codec falls back to H264 on same device", func(t *testing.T) {
		cfg := config.Default()
		cfg.VAAPIDevice = "/dev/dri/renderD128"
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		resolved, err := selectVAAPIConfig(cfg, choices, false, true, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoH264 || resolved.VAAPIDevice != "/dev/dri/renderD128" {
			t.Fatalf("expected H264 on /dev/dri/renderD128, got codec=%s device=%s", resolved.VideoCodec, resolved.VAAPIDevice)
		}
	})

	t.Run("case 3: implicit device + explicit HEVC selects device for requested codec", func(t *testing.T) {
		cfg := config.Default()
		cfg.VideoCodec = config.VideoHEVC
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
			{Codec: config.VideoHEVC, Device: "/dev/dri/renderD129"},
		}
		resolved, err := selectVAAPIConfig(cfg, choices, true, false, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoHEVC || resolved.VAAPIDevice != "/dev/dri/renderD129" {
			t.Fatalf("expected HEVC on /dev/dri/renderD129, got codec=%s device=%s", resolved.VideoCodec, resolved.VAAPIDevice)
		}
	})

	t.Run("case 4: explicit device + explicit HEVC selects exact pair", func(t *testing.T) {
		cfg := config.Default()
		cfg.VAAPIDevice = "/dev/dri/renderD128"
		cfg.VideoCodec = config.VideoHEVC
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"},
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		resolved, err := selectVAAPIConfig(cfg, choices, true, true, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoHEVC || resolved.VAAPIDevice != "/dev/dri/renderD128" {
			t.Fatalf("expected HEVC on /dev/dri/renderD128, got codec=%s device=%s", resolved.VideoCodec, resolved.VAAPIDevice)
		}
	})

	t.Run("case 5: explicit device + explicit HEVC errors when HEVC unavailable", func(t *testing.T) {
		cfg := config.Default()
		cfg.VAAPIDevice = "/dev/dri/renderD128"
		cfg.VideoCodec = config.VideoHEVC
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		_, err := selectVAAPIConfig(cfg, choices, true, true, 1920, 1080)
		if err == nil {
			t.Fatal("expected error when explicit HEVC is unavailable")
		}
		if !strings.Contains(err.Error(), "selected hevc VAAPI encoder is unavailable") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("case 6: explicit device + implicit codec errors when no usable choices", func(t *testing.T) {
		cfg := config.Default()
		cfg.VAAPIDevice = "/dev/dri/renderD128"
		choices := []ui.VAAPIChoice{}
		_, err := selectVAAPIConfig(cfg, choices, false, true, 1920, 1080)
		if err == nil {
			t.Fatal("expected error when no usable choices are available")
		}
		if !strings.Contains(err.Error(), "no usable VAAPI encoder for 1920x1080 on selected device") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("case 7: implicit device + implicit codec falls back to H264 and its device", func(t *testing.T) {
		cfg := config.Default()
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		resolved, err := selectVAAPIConfig(cfg, choices, false, false, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoH264 || resolved.VAAPIDevice != "/dev/dri/renderD128" {
			t.Fatalf("expected H264 on /dev/dri/renderD128, got codec=%s device=%s", resolved.VideoCodec, resolved.VAAPIDevice)
		}
	})

	t.Run("explicit device never silently switches to another device", func(t *testing.T) {
		cfg := config.Default()
		cfg.VAAPIDevice = "/dev/dri/renderD128"
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoHEVC, Device: "/dev/dri/renderD129"},
		}
		_, err := selectVAAPIConfig(cfg, choices, false, true, 1920, 1080)
		if err == nil {
			t.Fatal("expected error when requested device is not among choices")
		}
		if !strings.Contains(err.Error(), "no usable VAAPI encoder for 1920x1080 on selected device") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("explicit codec never silently switches to another codec", func(t *testing.T) {
		cfg := config.Default()
		cfg.VideoCodec = config.VideoH264
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"},
		}
		_, err := selectVAAPIConfig(cfg, choices, true, false, 1920, 1080)
		if err == nil {
			t.Fatal("expected error when explicit H264 is unavailable")
		}
		if !strings.Contains(err.Error(), "selected h264 VAAPI encoder is unavailable") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestSelectVAAPIRegressionDevicePopulatedFallback(t *testing.T) {
	// Regression test for the old bug:
	// User explicitly selects a VAAPI device (populated in cfg.VAAPIDevice)
	// but does NOT explicitly select a video codec (default HEVC in cfg.VideoCodec).
	// Only H.264 is available on that device.
	// The old logic skipped setting cfg.VideoCodec because cfg.VAAPIDevice != "".
	// The new logic must correctly fall back to H.264 while retaining the requested device.
	cfg := config.Default()
	cfg.VAAPIDevice = "/dev/dri/renderD128"
	codecExplicit := false
	deviceExplicit := true
	choices := []ui.VAAPIChoice{
		{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
	}

	resolved, err := selectVAAPIConfig(cfg, choices, codecExplicit, deviceExplicit, 1920, 1080)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.VideoCodec != config.VideoH264 {
		t.Fatalf("regression failure: expected VideoCodec to fall back to %q, got %q", config.VideoH264, resolved.VideoCodec)
	}
	if resolved.VAAPIDevice != "/dev/dri/renderD128" {
		t.Fatalf("expected VAAPIDevice to remain %q, got %q", "/dev/dri/renderD128", resolved.VAAPIDevice)
	}
}
