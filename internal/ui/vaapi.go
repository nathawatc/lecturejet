package ui

import (
	"fmt"
	"path/filepath"
	"sort"

	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/media"
)

type VAAPIChoice struct {
	Codec  config.VideoCodec
	Device string
}

// ProbeVAAPIChoices discovers usable VAAPI encoders for the probed input dimensions.
func ProbeVAAPIChoices(info media.ProbeInfo, preferred config.VideoCodec, requestedDevice string) []VAAPIChoice {
	var devices []string
	if requestedDevice != "" {
		devices = []string{requestedDevice}
	} else {
		devices, _ = filepath.Glob("/dev/dri/renderD*")
		sort.Strings(devices)
	}
	codecs := []config.VideoCodec{preferred}
	if preferred == config.VideoHEVC {
		codecs = append(codecs, config.VideoH264)
	} else {
		codecs = append(codecs, config.VideoHEVC)
	}
	choices := make([]VAAPIChoice, 0, len(codecs))
	for _, codec := range codecs {
		for _, device := range devices {
			if err := media.ProbeVAAPI(device, string(codec), info.VideoWidth, info.VideoHeight); err == nil {
				choices = append(choices, VAAPIChoice{Codec: codec, Device: device})
				break
			}
		}
	}
	return choices
}

// FilterChoices returns choices matching codec.
func FilterChoices(choices []VAAPIChoice, codec config.VideoCodec) []VAAPIChoice {
	filtered := make([]VAAPIChoice, 0, len(choices))
	for _, choice := range choices {
		if choice.Codec == codec {
			filtered = append(filtered, choice)
		}
	}
	return filtered
}

// CheckAvailable validates that candidate choices exist, producing actionable errors when not.
func CheckAvailable(choices []VAAPIChoice, codec config.VideoCodec, width, height int, codecExplicit, deviceExplicit bool) error {
	if len(choices) > 0 {
		return nil
	}
	if codecExplicit {
		return fmt.Errorf("selected %s VAAPI encoder is unavailable for this input/device", codec)
	}
	if deviceExplicit {
		return fmt.Errorf("no usable VAAPI encoder for %dx%d on selected device", width, height)
	}
	return fmt.Errorf("no usable VAAPI encoder for %dx%d", width, height)
}

// SelectVAAPI selects the codec and device according to explicit preferences and candidate choices.
func SelectVAAPI(
	cfg config.Config,
	choices []VAAPIChoice,
	codecExplicit bool,
	deviceExplicit bool,
	width int,
	height int,
) (config.Config, error) {
	for _, choice := range choices {
		if deviceExplicit && choice.Device != cfg.VAAPIDevice {
			continue
		}
		if codecExplicit && choice.Codec != cfg.VideoCodec {
			continue
		}
		if !codecExplicit {
			cfg.VideoCodec = choice.Codec
		}
		if !deviceExplicit {
			cfg.VAAPIDevice = choice.Device
		}
		return cfg, nil
	}
	return config.Config{}, CheckAvailable(nil, cfg.VideoCodec, width, height, codecExplicit, deviceExplicit)
}
