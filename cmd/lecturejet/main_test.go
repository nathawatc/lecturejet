package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/ui"
)

func TestRunEarlyValidationBeforeAnalysis(t *testing.T) {
	dir := t.TempDir()
	existingOutput := filepath.Join(dir, "existing.mp4")
	if err := os.WriteFile(existingOutput, []byte("existing content"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	t.Run("unsupported extension rejected before probing", func(t *testing.T) {
		err := run([]string{"input.mp4", "output.mkv", "--non-interactive"}, nil, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for .mkv extension, got nil")
		}
		if !strings.Contains(err.Error(), ".mp4 output only") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("same input and output rejected before probing", func(t *testing.T) {
		err := run([]string{"input.mp4", "./input.mp4", "--non-interactive"}, nil, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for same input and output, got nil")
		}
		if !strings.Contains(err.Error(), "must not refer to the same file") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("existing destination without force rejected before probing", func(t *testing.T) {
		err := run([]string{"input.mp4", existingOutput, "--non-interactive"}, nil, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected error for existing destination without --force, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("unexpected error message: %v", err)
		}
		data, readErr := os.ReadFile(existingOutput)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(data) != "existing content" {
			t.Fatalf("existing destination was modified: %q", string(data))
		}
	})

	t.Run("existing destination with force passes preflight and reaches probe", func(t *testing.T) {
		err := run([]string{filepath.Join(dir, "nonexistent-input.mp4"), existingOutput, "--force", "--non-interactive"}, nil, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected probe error on nonexistent input, got nil")
		}
		if !strings.Contains(err.Error(), "probe ") {
			t.Fatalf("expected failure at media probe, got: %v", err)
		}
		data, readErr := os.ReadFile(existingOutput)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(data) != "existing content" {
			t.Fatalf("existing destination was modified: %q", string(data))
		}
	})
}

func TestSelectVAAPIRegressionSingleDashExplicitCodecDoesNotFallback(t *testing.T) {
	cfg, _, explicit, err := parseRenderArgs([]string{
		"input.mp4", "output.mp4",
		"-video-codec", "hevc",
		"--non-interactive",
	})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !explicit {
		t.Fatalf("expected -video-codec hevc to be explicit")
	}

	choices := []ui.VAAPIChoice{
		{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
	}
	_, err = ui.SelectVAAPI(cfg, choices, explicit, false, 1920, 1080)
	if err == nil {
		t.Fatal("expected error preventing fallback when explicit HEVC is unavailable")
	}
	if !strings.Contains(err.Error(), "selected hevc VAAPI encoder is unavailable") {
		t.Fatalf("unexpected error message: %v", err)
	}

	cfgImplicit, _, explicitImplicit, err := parseRenderArgs([]string{
		"input.mp4", "output.mp4",
		"--non-interactive",
	})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if explicitImplicit {
		t.Fatalf("expected no codec flag to have explicit=false")
	}
	resolved, err := ui.SelectVAAPI(cfgImplicit, choices, explicitImplicit, false, 1920, 1080)
	if err != nil {
		t.Fatalf("expected implicit codec to fall back to H.264: %v", err)
	}
	if resolved.VideoCodec != config.VideoH264 {
		t.Fatalf("expected fallback to %s, got %s", config.VideoH264, resolved.VideoCodec)
	}
}

func TestSelectVAAPIConfig(t *testing.T) {
	t.Run("case 1: implicit device + implicit codec chooses preferred HEVC", func(t *testing.T) {
		cfg := config.Default()
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"},
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		resolved, err := ui.SelectVAAPI(cfg, choices, false, false, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoHEVC {
			t.Fatalf("expected VideoCodec %q, got %q", config.VideoHEVC, resolved.VideoCodec)
		}
		if resolved.VAAPIDevice != "/dev/dri/renderD128" {
			t.Fatalf("expected VAAPIDevice %q, got %q", "/dev/dri/renderD128", resolved.VAAPIDevice)
		}
	})

	t.Run("case 2: implicit device + implicit codec falls back to H.264", func(t *testing.T) {
		cfg := config.Default()
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		resolved, err := ui.SelectVAAPI(cfg, choices, false, false, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoH264 {
			t.Fatalf("expected VideoCodec %q, got %q", config.VideoH264, resolved.VideoCodec)
		}
		if resolved.VAAPIDevice != "/dev/dri/renderD128" {
			t.Fatalf("expected VAAPIDevice %q, got %q", "/dev/dri/renderD128", resolved.VAAPIDevice)
		}
	})

	t.Run("case 3: explicit device + implicit codec falls back to H.264 on device", func(t *testing.T) {
		cfg := config.Default()
		cfg.VAAPIDevice = "/dev/dri/renderD129"
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoH264, Device: "/dev/dri/renderD129"},
		}
		resolved, err := ui.SelectVAAPI(cfg, choices, false, true, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoH264 {
			t.Fatalf("expected VideoCodec %q, got %q", config.VideoH264, resolved.VideoCodec)
		}
		if resolved.VAAPIDevice != "/dev/dri/renderD129" {
			t.Fatalf("expected VAAPIDevice %q, got %q", "/dev/dri/renderD129", resolved.VAAPIDevice)
		}
	})

	t.Run("case 4: explicit codec + implicit device finds supported device", func(t *testing.T) {
		cfg := config.Default()
		cfg.VideoCodec = config.VideoHEVC
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoHEVC, Device: "/dev/dri/renderD129"},
		}
		resolved, err := ui.SelectVAAPI(cfg, choices, true, false, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoHEVC {
			t.Fatalf("expected VideoCodec %q, got %q", config.VideoHEVC, resolved.VideoCodec)
		}
		if resolved.VAAPIDevice != "/dev/dri/renderD129" {
			t.Fatalf("expected VAAPIDevice %q, got %q", "/dev/dri/renderD129", resolved.VAAPIDevice)
		}
	})

	t.Run("case 5: explicit codec + explicit device matching choice succeeds", func(t *testing.T) {
		cfg := config.Default()
		cfg.VideoCodec = config.VideoH264
		cfg.VAAPIDevice = "/dev/dri/renderD128"
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		resolved, err := ui.SelectVAAPI(cfg, choices, true, true, 1920, 1080)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved.VideoCodec != config.VideoH264 {
			t.Fatalf("expected VideoCodec %q, got %q", config.VideoH264, resolved.VideoCodec)
		}
		if resolved.VAAPIDevice != "/dev/dri/renderD128" {
			t.Fatalf("expected VAAPIDevice %q, got %q", "/dev/dri/renderD128", resolved.VAAPIDevice)
		}
	})

	t.Run("case 6: explicit codec unsupported fails without fallback", func(t *testing.T) {
		cfg := config.Default()
		cfg.VideoCodec = config.VideoHEVC
		choices := []ui.VAAPIChoice{
			{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		}
		_, err := ui.SelectVAAPI(cfg, choices, true, false, 1920, 1080)
		if err == nil {
			t.Fatal("expected error for explicit codec unsupported, got nil")
		}
		if !strings.Contains(err.Error(), "selected hevc VAAPI encoder is unavailable") {
			t.Fatalf("expected specific codec error, got: %v", err)
		}
	})

	t.Run("case 7: explicit device with no usable encoder fails", func(t *testing.T) {
		cfg := config.Default()
		cfg.VAAPIDevice = "/dev/dri/renderD128"
		var choices []ui.VAAPIChoice
		_, err := ui.SelectVAAPI(cfg, choices, false, true, 1920, 1080)
		if err == nil {
			t.Fatal("expected error for explicit device with no encoders, got nil")
		}
		if !strings.Contains(err.Error(), "no usable VAAPI encoder for 1920x1080 on selected device") {
			t.Fatalf("expected specific device error, got: %v", err)
		}
	})

	t.Run("case 8: completely empty choices fails with generic error", func(t *testing.T) {
		cfg := config.Default()
		var choices []ui.VAAPIChoice
		_, err := ui.SelectVAAPI(cfg, choices, false, false, 1920, 1080)
		if err == nil {
			t.Fatal("expected error for empty choices, got nil")
		}
		if !strings.Contains(err.Error(), "no usable VAAPI encoder for 1920x1080") {
			t.Fatalf("expected generic error, got: %v", err)
		}
	})
}

func TestSelectVAAPIRegressionDevicePopulatedFallback(t *testing.T) {
	cfg := config.Default()
	cfg.VAAPIDevice = "/dev/dri/renderD128"
	choices := []ui.VAAPIChoice{
		{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
	}
	resolved, err := ui.SelectVAAPI(cfg, choices, false, false, 1920, 1080)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.VideoCodec != config.VideoH264 {
		t.Fatalf("expected VideoCodec to fall back to %q, got %q", config.VideoH264, resolved.VideoCodec)
	}
	if resolved.VAAPIDevice != "/dev/dri/renderD128" {
		t.Fatalf("expected VAAPIDevice to remain %q, got %q", "/dev/dri/renderD128", resolved.VAAPIDevice)
	}
}

func TestInvalidCommandBehavior(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "empty args",
			args:    []string{},
			wantErr: "usage: lecturejet input.mp4 output.mp4",
		},
		{
			name:    "analyze missing input",
			args:    []string{"analyze"},
			wantErr: "usage: lecturejet analyze input.mp4",
		},
		{
			name:    "render missing output",
			args:    []string{"input.mp4"},
			wantErr: "usage: lecturejet input.mp4 output.mp4",
		},
		{
			name:    "analyze unknown flag",
			args:    []string{"analyze", "input.mp4", "--unknown-flag"},
			wantErr: "flag provided but not defined",
		},
		{
			name:    "render unknown flag",
			args:    []string{"input.mp4", "output.mp4", "--unknown-flag"},
			wantErr: "flag provided but not defined",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(tc.args, nil, &stdout, &stderr)
			if err == nil {
				t.Fatalf("expected error for args %v, got nil", tc.args)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
			if strings.Contains(err.Error(), "flag: help requested") {
				t.Fatalf("surfaced 'flag: help requested' as error: %v", err)
			}
		})
	}
}
