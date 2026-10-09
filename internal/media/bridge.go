//go:build cgo && linux

package media

/*
#cgo pkg-config: libavformat libavcodec libavutil libswresample libswscale libavfilter
#cgo CFLAGS: -I${SRCDIR}/../../native
#include "lecturejet_ffmpeg.h"
#include <stdlib.h>
int lj_render_file_go(const char *input, const char *output,
                      const LJSegment *segments, size_t segment_count,
                      const LJRenderOptions *options, char *error, size_t error_size);
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"

	"codeberg.org/nathawat/lecturejet/internal/timeline"
)

const errorBufferSize = 1024

type cancelHandle struct {
	ptr  *C.LJCancelToken
	stop chan struct{}
	done chan struct{}
}

func newCancelHandle(ctx context.Context) (*cancelHandle, error) {
	if ctx == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ctx.Done() == nil {
		return nil, nil
	}
	ptr := C.lj_cancel_token_create()
	if ptr == nil {
		return nil, fmt.Errorf("creating cancellation token: out of memory")
	}
	handle := &cancelHandle{ptr: ptr, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(handle.done)
		select {
		case <-ctx.Done():
			C.lj_cancel_token_cancel(handle.ptr)
		case <-handle.stop:
		}
	}()
	return handle, nil
}

func (handle *cancelHandle) close() {
	if handle == nil || handle.ptr == nil {
		return
	}
	close(handle.stop)
	<-handle.done
	C.lj_cancel_token_destroy((**C.LJCancelToken)(unsafe.Pointer(&handle.ptr)))
	handle.ptr = nil
}

func Probe(path string) (ProbeInfo, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var cInfo C.LJProbeInfo
	errorText := make([]byte, errorBufferSize)
	ret := C.lj_probe_file(cPath, &cInfo, (*C.char)(unsafe.Pointer(&errorText[0])), C.size_t(len(errorText)))
	if ret < 0 {
		return ProbeInfo{}, fmt.Errorf("%s", cString(errorText))
	}
	return ProbeInfo{
		DurationNS:  timeline.TimeNS(cInfo.duration_ns),
		Audio:       cInfo.audio_stream >= 0,
		Video:       cInfo.video_stream >= 0,
		VideoWidth:  int(cInfo.video_width),
		VideoHeight: int(cInfo.video_height),
		VideoCodec:  C.GoString(&cInfo.video_codec[0]),
	}, nil
}

func ProbeVAAPI(device, codec string, width, height int) error {
	cDevice := C.CString(device)
	defer C.free(unsafe.Pointer(cDevice))
	cCodec := C.CString(codec)
	defer C.free(unsafe.Pointer(cCodec))
	errorText := make([]byte, errorBufferSize)
	ret := C.lj_probe_vaapi(cDevice, cCodec, C.int(width), C.int(height),
		(*C.char)(unsafe.Pointer(&errorText[0])), C.size_t(len(errorText)))
	if ret < 0 {
		return fmt.Errorf("%s", cString(errorText))
	}
	return nil
}

type AudioDecoder struct {
	ptr      *C.LJAudioDecoder
	channels int
	cancel   *cancelHandle
}

func OpenAudio(ctx context.Context, path string, channels int, rnnoise bool, strength float64,
	model string) (*AudioDecoder, error) {
	cancel, err := newCancelHandle(ctx)
	if err != nil {
		return nil, err
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cModel := C.CString(model)
	defer C.free(unsafe.Pointer(cModel))
	errorText := make([]byte, errorBufferSize)
	var cancelPtr *C.LJCancelToken
	if cancel != nil {
		cancelPtr = cancel.ptr
	}
	ptr := C.lj_audio_open(cPath, C.int(channels), cancelPtr, boolInt(rnnoise), C.double(strength), cModel,
		(*C.char)(unsafe.Pointer(&errorText[0])), C.size_t(len(errorText)))
	if ptr == nil {
		cancel.close()
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s", cString(errorText))
	}
	return &AudioDecoder{ptr: ptr, channels: channels, cancel: cancel}, nil
}

func (d *AudioDecoder) Read(dst []float32) (samples int, start int64, err error) {
	if d == nil || d.ptr == nil || len(dst) == 0 || len(dst)%d.channels != 0 {
		return 0, 0, fmt.Errorf("reading audio: invalid destination")
	}
	errorText := make([]byte, errorBufferSize)
	maxSamples := len(dst) / d.channels
	ret := C.lj_audio_read(d.ptr, (*C.float)(unsafe.Pointer(&dst[0])), C.int(maxSamples),
		(*C.int64_t)(unsafe.Pointer(&start)), (*C.char)(unsafe.Pointer(&errorText[0])),
		C.size_t(len(errorText)))
	if ret < 0 {
		return 0, 0, fmt.Errorf("%s", cString(errorText))
	}
	return int(ret), start, nil
}

func (d *AudioDecoder) Close() {
	if d == nil {
		return
	}
	if d.ptr != nil {
		C.lj_audio_close((**C.LJAudioDecoder)(unsafe.Pointer(&d.ptr)))
		d.ptr = nil
	}
	d.cancel.close()
	d.cancel = nil
}

func Render(ctx context.Context, input, output string, source timeline.Timeline,
	options RenderOptions, progress func(int)) (renderError error) {
	segments := source.Segments()
	if len(segments) == 0 {
		return fmt.Errorf("rendering: timeline contains no segments")
	}
	cancel, err := newCancelHandle(ctx)
	if err != nil {
		return err
	}
	defer cancel.close()
	withRenderCall(func() {
		cInput := C.CString(input)
		defer C.free(unsafe.Pointer(cInput))
		cOutput := C.CString(output)
		defer C.free(unsafe.Pointer(cOutput))
		cCodec := C.CString(options.VideoCodec)
		defer C.free(unsafe.Pointer(cCodec))
		cTag := C.CString(options.VideoTag)
		defer C.free(unsafe.Pointer(cTag))
		cDevice := C.CString(options.VAAPIDevice)
		defer C.free(unsafe.Pointer(cDevice))
		cModel := C.CString(options.RNNoiseModel)
		defer C.free(unsafe.Pointer(cModel))
		var cancelPtr *C.LJCancelToken
		if cancel != nil {
			cancelPtr = cancel.ptr
		}
		cSegments := make([]C.LJSegment, len(segments))
		for i, segment := range segments {
			cSegments[i] = C.LJSegment{start_ns: C.int64_t(segment.Start), end_ns: C.int64_t(segment.End), action: C.int(segment.Action)}
		}
		cOptions := C.LJRenderOptions{
			video_codec:      cCodec,
			video_tag:        cTag,
			vaapi_device:     cDevice,
			output_mono:      boolInt(options.OutputMono),
			rnnoise_enabled:  boolInt(options.RNNoiseEnabled),
			rnnoise_strength: C.double(options.RNNoiseStrength),
			rnnoise_model:    cModel,
			cancel:           cancelPtr,
		}
		errorText := make([]byte, errorBufferSize)
		setRenderProgress(progress)
		defer setRenderProgress(nil)
		ret := C.lj_render_file_go(cInput, cOutput, (*C.LJSegment)(unsafe.Pointer(&cSegments[0])), C.size_t(len(cSegments)),
			&cOptions, (*C.char)(unsafe.Pointer(&errorText[0])), C.size_t(len(errorText)))
		if ctx != nil && ctx.Err() != nil {
			renderError = ctx.Err()
			return
		}
		if ret < 0 {
			renderError = fmt.Errorf("%s", cString(errorText))
			return
		}
	})
	return renderError
}

func RemuxHEVCHVC1(ctx context.Context, input, output string) (remuxError error) {
	cancel, err := newCancelHandle(ctx)
	if err != nil {
		return err
	}
	defer cancel.close()
	withRenderCall(func() {
		cInput := C.CString(input)
		defer C.free(unsafe.Pointer(cInput))
		cOutput := C.CString(output)
		defer C.free(unsafe.Pointer(cOutput))
		var cancelPtr *C.LJCancelToken
		if cancel != nil {
			cancelPtr = cancel.ptr
		}
		errorText := make([]byte, errorBufferSize)
		ret := C.lj_remux_hevc_hvc1(cInput, cOutput, cancelPtr,
			(*C.char)(unsafe.Pointer(&errorText[0])), C.size_t(len(errorText)))
		if ctx != nil && ctx.Err() != nil {
			remuxError = ctx.Err()
			return
		}
		if ret < 0 {
			remuxError = fmt.Errorf("%s", cString(errorText))
		}
	})
	return remuxError
}

func boolInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func cString(value []byte) string {
	for i, byteValue := range value {
		if byteValue == 0 {
			return string(value[:i])
		}
	}
	return string(value)
}
