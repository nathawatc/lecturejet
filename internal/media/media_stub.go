//go:build !cgo || !linux

package media

import (
	"context"
	"errors"

	"codeberg.org/nathawat/lecturejet/internal/timeline"
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

func OpenAudio(ctx context.Context, path string, channels int, rnnoise bool, strength float64, model string) (*AudioDecoder, error) {
	return nil, errUnsupported
}

func Render(ctx context.Context, input, output string, source timeline.Timeline, options RenderOptions, progress func(int)) error {
	return errUnsupported
}

func RemuxHEVCHVC1(ctx context.Context, input, output string) error {
	return errUnsupported
}
