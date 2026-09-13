package media

import "lecturejet/internal/timeline"

type ProbeInfo struct {
	DurationNS    timeline.TimeNS
	Audio         bool
	AudioRate     int
	AudioChannels int
	Video         bool
	VideoWidth    int
	VideoHeight   int
	VideoFPSNum   int
	VideoFPSDen   int
	AudioCodec    string
	VideoCodec    string
}

type RenderOptions struct {
	VideoCodec      string
	VideoTag        string
	VAAPIDevice     string
	OutputMono      bool
	RNNoiseEnabled  bool
	RNNoiseStrength float64
	RNNoiseModel    string
}
