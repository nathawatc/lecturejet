package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime/debug"
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
		cfg.HEVCCodecTag != config.HEVCCodecTagHVC1 || cfg.Audio.Mono || cfg.Threads != 0 || cfg.Force {
		t.Fatalf("unexpected parsed config: %+v interactive=%v explicit=%v", cfg, interactive, explicit)
	}
}

func TestParseRenderForceFlag(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantForce bool
	}{
		{
			name:      "default no force",
			args:      []string{"input.mp4", "output.mp4"},
			wantForce: false,
		},
		{
			name:      "--force flag present",
			args:      []string{"input.mp4", "output.mp4", "--force"},
			wantForce: true,
		},
		{
			name:      "--force=true",
			args:      []string{"input.mp4", "output.mp4", "--force=true"},
			wantForce: true,
		},
		{
			name:      "--force=false",
			args:      []string{"input.mp4", "output.mp4", "--force=false"},
			wantForce: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, _, err := parseRenderArgs(tc.args)
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if cfg.Force != tc.wantForce {
				t.Fatalf("expected Force=%v, got %v", tc.wantForce, cfg.Force)
			}
		})
	}
}

func TestParseAnalyzeRejectsForceFlag(t *testing.T) {
	if _, err := parseAnalyzeArgs([]string{"input.mp4", "--force"}); err == nil {
		t.Fatal("accepted invalid --force flag in analyze")
	}
}

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
		// Content must remain untouched
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
		// Should fail on probing input, NOT on output-existence
		if !strings.Contains(err.Error(), "probe ") {
			t.Fatalf("expected failure at media probe, got: %v", err)
		}
		// Content must remain untouched
		data, readErr := os.ReadFile(existingOutput)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(data) != "existing content" {
			t.Fatalf("existing destination was modified: %q", string(data))
		}
	})
}

func TestParseRenderVideoCodecExplicitness(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantExplicit bool
		wantCodec    config.VideoCodec
	}{
		{
			name:         "no video-codec flag",
			args:         []string{"input.mp4", "output.mp4"},
			wantExplicit: false,
			wantCodec:    config.VideoHEVC,
		},
		{
			name:         "--video-codec h264",
			args:         []string{"input.mp4", "output.mp4", "--video-codec", "h264"},
			wantExplicit: true,
			wantCodec:    config.VideoH264,
		},
		{
			name:         "--video-codec=h264",
			args:         []string{"input.mp4", "output.mp4", "--video-codec=h264"},
			wantExplicit: true,
			wantCodec:    config.VideoH264,
		},
		{
			name:         "-video-codec h264",
			args:         []string{"input.mp4", "output.mp4", "-video-codec", "h264"},
			wantExplicit: true,
			wantCodec:    config.VideoH264,
		},
		{
			name:         "-video-codec=h264",
			args:         []string{"input.mp4", "output.mp4", "-video-codec=h264"},
			wantExplicit: true,
			wantCodec:    config.VideoH264,
		},
		{
			name:         "--video-codec hevc (explicit default)",
			args:         []string{"input.mp4", "output.mp4", "--video-codec", "hevc"},
			wantExplicit: true,
			wantCodec:    config.VideoHEVC,
		},
		{
			name:         "-video-codec hevc (explicit default)",
			args:         []string{"input.mp4", "output.mp4", "-video-codec", "hevc"},
			wantExplicit: true,
			wantCodec:    config.VideoHEVC,
		},
		{
			name:         "--video-codec=hevc (explicit default)",
			args:         []string{"input.mp4", "output.mp4", "--video-codec=hevc"},
			wantExplicit: true,
			wantCodec:    config.VideoHEVC,
		},
		{
			name:         "-video-codec=hevc (explicit default)",
			args:         []string{"input.mp4", "output.mp4", "-video-codec=hevc"},
			wantExplicit: true,
			wantCodec:    config.VideoHEVC,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, explicit, err := parseRenderArgs(tc.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if explicit != tc.wantExplicit {
				t.Fatalf("expected explicit=%v, got %v", tc.wantExplicit, explicit)
			}
			if cfg.VideoCodec != tc.wantCodec {
				t.Fatalf("expected codec=%s, got %s", tc.wantCodec, cfg.VideoCodec)
			}
		})
	}
}

func TestParseRenderCodecRegressionValueComparisonInsufficient(t *testing.T) {
	// Demonstrates why comparing parsed value against default is insufficient:
	// Default codec is HEVC. If a user explicitly supplies -video-codec hevc,
	// the parsed value equals the default value, but explicit must still be true.
	cfg, _, explicit, err := parseRenderArgs([]string{"input.mp4", "output.mp4", "-video-codec", "hevc"})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	defaultCodec := config.Default().VideoCodec
	if cfg.VideoCodec != defaultCodec {
		t.Fatalf("expected parsed codec %q to match default %q", cfg.VideoCodec, defaultCodec)
	}
	if !explicit {
		t.Fatalf("regression failure: explicitly supplied default codec must have explicit=true, got false")
	}
}

func TestSelectVAAPIRegressionSingleDashExplicitCodecDoesNotFallback(t *testing.T) {
	// End-to-end integration of parser + selection:
	// Single-dash -video-codec hevc must mark explicit=true, which prevents
	// silent fallback to H.264 when HEVC is unavailable.
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
	_, err = selectVAAPIConfig(cfg, choices, explicit, false, 1920, 1080)
	if err == nil {
		t.Fatal("expected error preventing fallback when explicit HEVC is unavailable")
	}
	if !strings.Contains(err.Error(), "selected hevc VAAPI encoder is unavailable") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// Contrast with no codec flag (implicit): fallback to H.264 must succeed.
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
	resolved, err := selectVAAPIConfig(cfgImplicit, choices, explicitImplicit, false, 1920, 1080)
	if err != nil {
		t.Fatalf("expected implicit codec to fall back to H.264: %v", err)
	}
	if resolved.VideoCodec != config.VideoH264 {
		t.Fatalf("expected fallback to %s, got %s", config.VideoH264, resolved.VideoCodec)
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

func TestTopLevelHelp(t *testing.T) {
	for _, flagName := range []string{"--help", "-h"} {
		t.Run(flagName, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run([]string{flagName}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("expected nil error for %s, got: %v", flagName, err)
			}
			out := stdout.String()
			if !strings.Contains(out, "Usage:") {
				t.Fatalf("expected Usage: in stdout, got:\n%s", out)
			}
			if !strings.Contains(out, "lecturejet <input.mp4> <output.mp4> [options]") {
				t.Fatalf("expected render invocation in stdout, got:\n%s", out)
			}
			if !strings.Contains(out, "analyze") {
				t.Fatalf("expected mention of analyze in stdout, got:\n%s", out)
			}
			// Must expose render flags from FlagSet
			for _, expectedFlag := range []string{
				"-force",
				"-threads",
				"-video-codec",
				"-hevc-tag",
				"-vaapi-device",
				"-rnnoise",
				"-rnnoise-model",
				"-rnnoise-strength",
				"-mono",
				"-stereo",
				"-non-interactive",
				"-threshold",
				"-min-silence",
				"-margin",
			} {
				if !strings.Contains(out, expectedFlag) {
					t.Errorf("expected %s in render help, got:\n%s", expectedFlag, out)
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got: %s", stderr.String())
			}
		})
	}
}

func TestAnalyzeHelp(t *testing.T) {
	for _, flagName := range []string{"--help", "-h"} {
		t.Run(flagName, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run([]string{"analyze", flagName}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("expected nil error for analyze %s, got: %v", flagName, err)
			}
			out := stdout.String()
			if !strings.Contains(out, "Usage:") {
				t.Fatalf("expected Usage: in stdout, got:\n%s", out)
			}
			if !strings.Contains(out, "lecturejet analyze <input.mp4> [options]") {
				t.Fatalf("expected analyze invocation in stdout, got:\n%s", out)
			}
			for _, expectedFlag := range []string{
				"-threads",
				"-rnnoise",
				"-rnnoise-model",
				"-rnnoise-strength",
				"-threshold",
				"-min-silence",
				"-margin",
			} {
				if !strings.Contains(out, expectedFlag) {
					t.Errorf("expected %s in analyze help, got:\n%s", expectedFlag, out)
				}
			}
			// Must NOT contain render-only flags
			for _, forbiddenFlag := range []string{
				"-force",
				"-video-codec",
				"-hevc-tag",
				"-vaapi-device",
				"-mono",
				"-stereo",
				"-non-interactive",
			} {
				if strings.Contains(out, forbiddenFlag) {
					t.Errorf("did not expect render-only flag %s in analyze help, got:\n%s", forbiddenFlag, out)
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got: %s", stderr.String())
			}
		})
	}
}

func TestHelpBeforeExpensiveWork(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// Intentional nonexistent paths: help commands must not probe or preflight
	err := run([]string{"--help"}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	err = run([]string{"analyze", "--help"}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestParserHelp(t *testing.T) {
	t.Run("render parser help with positional args", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run([]string{"nonexistent-input.mp4", "nonexistent-output.mp4", "--help"}, nil, &stdout, &stderr)
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "Usage:") || !strings.Contains(out, "-force") {
			t.Fatalf("expected render help in stdout, got:\n%s", out)
		}
	})

	t.Run("analyze parser help with positional args", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run([]string{"analyze", "nonexistent-input.mp4", "--help"}, nil, &stdout, &stderr)
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "Usage:") || !strings.Contains(out, "-threshold") || strings.Contains(out, "-force") {
			t.Fatalf("expected analyze help in stdout, got:\n%s", out)
		}
	})
}

func TestVersion(t *testing.T) {
	for _, flagName := range []string{"--version", "-version"} {
		t.Run(flagName, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run([]string{flagName}, nil, &stdout, &stderr)
			if err != nil {
				t.Fatalf("expected nil error for %s, got: %v", flagName, err)
			}
			out := stdout.String()
			if !strings.HasPrefix(out, "lecturejet ") {
				t.Fatalf("expected stdout starting with 'lecturejet ', got:\n%s", out)
			}
			if !strings.HasSuffix(out, "\n") {
				t.Fatalf("expected newline termination, got: %q", out)
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got: %s", stderr.String())
			}
		})
	}
}

func TestVersionFormatters(t *testing.T) {
	formatTests := []struct {
		input string
		want  string
	}{
		{"v0.1.0", "0.1.0"},
		{"0.1.0", "0.1.0"},
		{"v1.2.3-rc.1", "1.2.3-rc.1"},
		{"(devel)", "dev"},
		{"", "dev"},
		{"   v0.1.0\n", "0.1.0"},
	}
	for _, tc := range formatTests {
		if got := formatVersion(tc.input); got != tc.want {
			t.Errorf("formatVersion(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}

	if got := versionFromBuildInfo(nil); got != "dev" {
		t.Errorf("versionFromBuildInfo(nil) = %q; want %q", got, "dev")
	}

	infoDevel := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
	}
	if got := versionFromBuildInfo(infoDevel); got != "dev" {
		t.Errorf("versionFromBuildInfo(devel) = %q; want %q", got, "dev")
	}

	infoTagged := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0"},
	}
	if got := versionFromBuildInfo(infoTagged); got != "0.1.0" {
		t.Errorf("versionFromBuildInfo(v0.1.0) = %q; want %q", got, "0.1.0")
	}

	origOverride := versionOverride
	defer func() { versionOverride = origOverride }()
	versionOverride = "v0.2.0"
	if got := currentVersion(); got != "0.2.0" {
		t.Errorf("currentVersion() with override = %q; want %q", got, "0.2.0")
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
			// Help requested must NOT be surfaced as error
			if strings.Contains(err.Error(), "flag: help requested") {
				t.Fatalf("surfaced 'flag: help requested' as error: %v", err)
			}
		})
	}
}

func TestFlagErrHelpHandling(t *testing.T) {
	_, _, _, err := parseRenderArgs([]string{"input.mp4", "output.mp4", "--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp from parseRenderArgs, got: %v", err)
	}

	_, err = parseAnalyzeArgs([]string{"input.mp4", "--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp from parseAnalyzeArgs, got: %v", err)
	}

	_, err = parseAnalyzeArgs([]string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp from parseAnalyzeArgs(--help), got: %v", err)
	}
}
