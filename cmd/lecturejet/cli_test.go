package main

import (
	"bytes"
	"errors"
	"flag"
	"runtime/debug"
	"strings"
	"testing"

	"codeberg.org/nathawat/lecturejet/internal/config"
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
			name:         "-video-codec h264",
			args:         []string{"input.mp4", "output.mp4", "-video-codec", "h264"},
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

	versionOverride = "v9.9.9"
	if got := versionFromBuildInfo(infoTagged); got != "0.1.0" {
		t.Errorf("versionFromBuildInfo should be pure and ignore versionOverride; got %q, want %q", got, "0.1.0")
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
