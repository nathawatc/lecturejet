//go:build !cgo || !linux

package media

import (
	"context"
	"errors"

	"lecturejet/internal/timeline"
)

var errUnsupported = errors.New("media operations require Linux with cgo and VAAPI")

type AudioDecoder struct{}

func (d *AudioDecoder) Read(dst []float32) (samples int, start int64, err error) {
	return 0, 0, errUnsupported
}

func (d *AudioDecoder) Close() {}

func Probe(path string) (ProbeInfo, error) {
	return ProbeInfo{}, errUnsupported
}

func ProbeVAAPI(device, codec string, width, height int) error {
	return errUnsupported
}

func OpenAudio(path string, channels int, rnnoise bool, strength float64, model string) (*AudioDecoder, error) {
	return nil, errUnsupported
}

func OpenAudioWithContext(ctx context.Context, path string, channels int, rnnoise bool, strength float64, model string) (*AudioDecoder, error) {
	return nil, errUnsupported
}

func RenderWithProgressContext(ctx context.Context, input, output string, source timeline.Timeline, options RenderOptions, progress func(int)) error {
	return errUnsupported
}

func RemuxHEVCHVC1WithContext(ctx context.Context, input, output string) error {
	return errUnsupported
}
