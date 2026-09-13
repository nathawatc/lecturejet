package ui

import (
	"strings"
	"testing"

	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/media"
)

func TestFilterChoices(t *testing.T) {
	choices := []VAAPIChoice{
		{Codec: config.VideoHEVC, Device: "/dev/dri/renderD128"},
		{Codec: config.VideoH264, Device: "/dev/dri/renderD128"},
		{Codec: config.VideoHEVC, Device: "/dev/dri/renderD129"},
	}
	filtered := FilterChoices(choices, config.VideoHEVC)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 HEVC choices, got %d", len(filtered))
	}
	for _, c := range filtered {
		if c.Codec != config.VideoHEVC {
			t.Fatalf("unexpected codec in filtered: %v", c.Codec)
		}
	}
}

func TestCheckAvailableErrors(t *testing.T) {
	if err := CheckAvailable([]VAAPIChoice{{Codec: config.VideoH264}}, config.VideoH264, 1920, 1080, false, false); err != nil {
		t.Fatalf("expected nil error when choices available, got: %v", err)
	}

	err := CheckAvailable(nil, config.VideoHEVC, 1920, 1080, true, false)
	if err == nil || !strings.Contains(err.Error(), "selected hevc VAAPI encoder is unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}

	err = CheckAvailable(nil, config.VideoH264, 1920, 1080, false, true)
	if err == nil || !strings.Contains(err.Error(), "on selected device") {
		t.Fatalf("unexpected error: %v", err)
	}

	err = CheckAvailable(nil, config.VideoH264, 1920, 1080, false, false)
	if err == nil || strings.Contains(err.Error(), "selected device") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProbeVAAPIChoicesNonExistentDevice(t *testing.T) {
	info := media.ProbeInfo{
		VideoWidth:  1920,
		VideoHeight: 1080,
	}
	choices := ProbeVAAPIChoices(info, config.VideoHEVC, "/dev/dri/nonexistent-device-999")
	if len(choices) != 0 {
		t.Fatalf("expected 0 choices for nonexistent device, got: %v", choices)
	}
}
