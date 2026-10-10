package config

import (
	"strings"
	"testing"
)

func TestDefaultLectureSettings(t *testing.T) {
	cfg := Default()
	if cfg.VideoCodec != VideoHEVC || cfg.HEVCCodecTag != HEVCCodecTagHEV1 ||
		!cfg.Audio.Mono || cfg.Noise.Strength != 100 || cfg.Force {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Silence.WindowMS != 10 || cfg.Silence.ThresholdDB != -38 ||
		cfg.Silence.MinSilenceMS != 450 || cfg.Silence.MarginMS != 150 {
		t.Fatalf("unexpected silence defaults: %+v", cfg.Silence)
	}
}

func TestValidation(t *testing.T) {
	base := Default()
	base.Input = "in.mp4"
	base.Output = "out.mp4"
	for name, mutate := range map[string]func(*Config){
		"negative threads": func(c *Config) { c.Threads = -1 },
		"bad strength":     func(c *Config) { c.Noise.Strength = 101 },
		"bad codec":        func(c *Config) { c.VideoCodec = "vp9" },
		"bad HEVC tag":     func(c *Config) { c.HEVCCodecTag = "avc1" },
		"bad min silence":  func(c *Config) { c.Silence.MinSilenceMS = 1 },
		"bad margin":       func(c *Config) { c.Silence.MarginMS = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	base.Noise.Enabled = true
	base.Noise.Strength = 0
	base.HEVCCodecTag = HEVCCodecTagHVC1
	if err := base.Validate(); err != nil {
		t.Fatalf("valid HEVC tag and zero strength rejected: %v", err)
	}
}

func TestRNNoiseValidationErrorCapitalization(t *testing.T) {
	cfg := Default()
	cfg.Input = "in.mp4"
	cfg.Output = "out.mp4"
	cfg.Noise.Strength = 101
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "RNNoise strength must be between 0 and 100") {
		t.Fatalf("expected capitalized RNNoise strength error, got: %v", err)
	}

	cfg = Default()
	cfg.Input = "in.mp4"
	cfg.Output = "out.mp4"
	cfg.Noise.Enabled = true
	cfg.Audio.Mono = false
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "RNNoise requires mono output") {
		t.Fatalf("expected capitalized RNNoise requires mono error, got: %v", err)
	}
}

func TestOutputExtensionValidation(t *testing.T) {
	tests := []struct {
		output  string
		wantErr bool
	}{
		{"out.mp4", false},
		{"out.MP4", false},
		{"out.mkv", true},
		{"out.mov", true},
		{"out", true},
	}
	for _, tc := range tests {
		t.Run(tc.output, func(t *testing.T) {
			cfg := Default()
			cfg.Input = "in.mp4"
			cfg.Output = tc.output
			err := cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for output %q, got nil", tc.output)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for output %q: %v", tc.output, err)
			}
		})
	}
}

func TestNoiseChannelValidationMatrix(t *testing.T) {
	cases := []struct {
		name         string
		noiseEnabled bool
		mono         bool
		wantErr      bool
	}{
		{name: "rnnoise off + mono", noiseEnabled: false, mono: true, wantErr: false},
		{name: "rnnoise off + stereo", noiseEnabled: false, mono: false, wantErr: false},
		{name: "rnnoise on + mono", noiseEnabled: true, mono: true, wantErr: false},
		{name: "rnnoise on + stereo", noiseEnabled: true, mono: false, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Input = "in.mp4"
			cfg.Output = "out.mp4"
			cfg.Noise.Enabled = tc.noiseEnabled
			cfg.Audio.Mono = tc.mono
			err := cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error for %s: %v", tc.name, err)
			}
		})
	}
}
