package main

import (
	"testing"

	"lecturejet/internal/config"
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
	} {
		if _, _, _, err := parseRenderArgs(args); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
}
