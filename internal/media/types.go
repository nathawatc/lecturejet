package media

import "codeberg.org/nathawat/lecturejet/internal/timeline"

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
}
