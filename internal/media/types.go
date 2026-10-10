package media

import (
	"codeberg.org/nathawat/lecturejet/internal/config"
	"codeberg.org/nathawat/lecturejet/internal/timeline"
)

func VAAPIEncoderName(codec config.VideoCodec) string {
	if codec == config.VideoHEVC {
		return "hevc_vaapi"
	}
	return "h264_vaapi"
}

type ProbeInfo struct {
	DurationNS  timeline.TimeNS
	Audio       bool
	Video       bool
	VideoWidth  int
	VideoHeight int
	VideoCodec  string
}

type RenderOptions struct {
	VideoCodec      string
	VideoTag        string
	VAAPIDevice     string
	OutputMono      bool
	RNNoiseEnabled  bool
	RNNoiseStrength float64
	RNNoiseModel    string
	Threads         int
}
