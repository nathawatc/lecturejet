package config

import (
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
)

type VideoCodec string

const (
	SampleRate            = 48000
	VideoHEVC  VideoCodec = "hevc"
	VideoH264  VideoCodec = "h264"
)

type HEVCCodecTag string

const (
	HEVCCodecTagHEV1 HEVCCodecTag = "hev1"
	HEVCCodecTagHVC1 HEVCCodecTag = "hvc1"
)

type AudioConfig struct {
	Mono bool
}

type NoiseConfig struct {
	Enabled  bool
	Strength int
	Model    string
}

type SilenceConfig struct {
	WindowMS     int
	ThresholdDB  float64
	MinSilenceMS int
	MarginMS     int
}

type Config struct {
	Input        string
	Output       string
	Force        bool
	Threads      int
	VideoCodec   VideoCodec
	HEVCCodecTag HEVCCodecTag
	VAAPIDevice  string
	Audio        AudioConfig
	Noise        NoiseConfig
	Silence      SilenceConfig
}

func Default() Config {
	return Config{
		Force:        false,
		Threads:      runtime.NumCPU(),
		VideoCodec:   VideoHEVC,
		HEVCCodecTag: HEVCCodecTagHEV1,
		Audio:        AudioConfig{Mono: true},
		Noise:        NoiseConfig{Strength: 80},
		Silence: SilenceConfig{
			WindowMS:     10,
			ThresholdDB:  -38,
			MinSilenceMS: 450,
			MarginMS:     150,
		},
	}
}

func (c Config) Validate() error {
	if c.Input == "" {
		return fmt.Errorf("input path is required")
	}
	if c.Output == "" {
		return fmt.Errorf("output path is required")
	}
	if strings.ToLower(filepath.Ext(c.Output)) != ".mp4" {
		return fmt.Errorf("unsupported output format %q: v0.1.0 supports .mp4 output only", c.Output)
	}
	if c.Threads < 0 {
		return fmt.Errorf("threads cannot be negative")
	}
	if c.VideoCodec != VideoHEVC && c.VideoCodec != VideoH264 {
		return fmt.Errorf("unsupported video codec %q: choose hevc or h264", c.VideoCodec)
	}
	if c.HEVCCodecTag != HEVCCodecTagHEV1 && c.HEVCCodecTag != HEVCCodecTagHVC1 {
		return fmt.Errorf("unsupported HEVC codec tag %q: choose hev1 or hvc1", c.HEVCCodecTag)
	}
	if c.Noise.Strength < 0 || c.Noise.Strength > 100 {
		return fmt.Errorf("RNNoise strength must be between 0 and 100")
	}
	if c.Noise.Enabled && !c.Audio.Mono {
		return fmt.Errorf("RNNoise requires mono output: stereo output cannot currently be combined with RNNoise")
	}
	if c.Silence.WindowMS < 1 {
		return fmt.Errorf("silence window must be at least 1 ms")
	}
	if c.Silence.MinSilenceMS < c.Silence.WindowMS {
		return fmt.Errorf("minimum silence must be at least one analysis window")
	}
	if c.Silence.MarginMS < 0 {
		return fmt.Errorf("speech margin cannot be negative")
	}
	if math.IsNaN(c.Silence.ThresholdDB) || math.IsInf(c.Silence.ThresholdDB, 0) {
		return fmt.Errorf("silence levels must be finite")
	}
	return nil
}

func (c Config) AnalysisSampleRate() int { return SampleRate }

func (c Config) NoiseStrength() float64 { return float64(c.Noise.Strength) / 100 }
