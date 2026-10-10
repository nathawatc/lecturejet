#include "vaapi_device.h"

#include <libavutil/hwcontext.h>

int lj_vaapi_device_open(const char *device, AVBufferRef **device_ref,
	char *error, size_t error_size)
{
	int ret = av_hwdevice_ctx_create(device_ref, AV_HWDEVICE_TYPE_VAAPI, device, NULL, 0);
	if (ret < 0)
		lj_fferror(error, error_size, "opening VAAPI device", ret);
	return ret;
}

int lj_vaapi_frames_alloc(AVBufferRef *device_ref, enum AVPixelFormat sw_format,
	int width, int height, int pool_size, AVBufferRef **frames_ref)
{
	*frames_ref = NULL;
	if (!device_ref || width <= 0 || height <= 0)
		return AVERROR(EINVAL);
	AVBufferRef *ref = av_hwframe_ctx_alloc(device_ref);
	if (!ref)
		return AVERROR(ENOMEM);
	AVHWFramesContext *frames = (AVHWFramesContext *)ref->data;
	frames->format = AV_PIX_FMT_VAAPI;
	frames->sw_format = sw_format;
	frames->width = width;
	frames->height = height;
	frames->initial_pool_size = pool_size;
	int ret = av_hwframe_ctx_init(ref);
	if (ret < 0) {
		av_buffer_unref(&ref);
		return ret;
	}
	*frames_ref = ref;
	return 0;
}

enum AVPixelFormat lj_vaapi_frame_sw_format(const AVFrame *frame)
{
	if (frame->format != AV_PIX_FMT_VAAPI || !frame->hw_frames_ctx)
		return AV_PIX_FMT_NONE;
	return ((const AVHWFramesContext *)frame->hw_frames_ctx->data)->sw_format;
}

enum AVPixelFormat lj_vaapi_get_format(AVCodecContext *context, const enum AVPixelFormat *formats)
{
	(void)context;
	for (const enum AVPixelFormat *format = formats; *format != AV_PIX_FMT_NONE; ++format) {
		if (*format == AV_PIX_FMT_VAAPI)
			return AV_PIX_FMT_VAAPI;
	}
	return formats[0];
}
