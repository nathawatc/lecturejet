#include "internal.h"

void lj_error(char *error, size_t error_size, const char *format, ...)
{
	if (!error || error_size == 0)
		return;
	va_list args;
	va_start(args, format);
	vsnprintf(error, error_size, format, args);
	va_end(args);
	error[error_size - 1] = '\0';
}

LJCancelToken *lj_cancel_token_create(void)
{
	return calloc(1, sizeof(LJCancelToken));
}

void lj_cancel_token_cancel(LJCancelToken *token)
{
	if (token)
		__atomic_store_n(&token->cancelled, 1, __ATOMIC_RELEASE);
}

void lj_cancel_token_destroy(LJCancelToken **token)
{
	if (!token || !*token)
		return;
	free(*token);
	*token = NULL;
}

int lj_cancel_requested(const LJCancelToken *token)
{
	return token && __atomic_load_n(&token->cancelled, __ATOMIC_ACQUIRE);
}

static int lj_ffmpeg_interrupt(void *opaque)
{
	return lj_cancel_requested((const LJCancelToken *)opaque);
}

int lj_abort_if_cancelled(LJCancelToken *token, char *error, size_t error_size)
{
	if (!lj_cancel_requested(token))
		return 0;
	lj_error(error, error_size, "operation cancelled");
	return AVERROR_EXIT;
}

void lj_set_interrupt_callback(AVFormatContext *format, LJCancelToken *token)
{
	if (!format || !token)
		return;
	format->interrupt_callback.callback = lj_ffmpeg_interrupt;
	format->interrupt_callback.opaque = token;
}

void lj_fferror(char *error, size_t error_size, const char *operation, int code)
{
	char detail[AV_ERROR_MAX_STRING_SIZE];
	av_strerror(code, detail, sizeof(detail));
	lj_error(error, error_size, "%s: %s", operation, detail);
}

uint32_t lj_fourcc(const char tag[4])
{
	return (uint32_t)(unsigned char)tag[0] |
		((uint32_t)(unsigned char)tag[1] << 8) |
		((uint32_t)(unsigned char)tag[2] << 16) |
		((uint32_t)(unsigned char)tag[3] << 24);
}

int lj_set_video_quality(AVCodecContext *context, char *error, size_t error_size)
{
	context->global_quality = LJ_DEFAULT_VIDEO_QP;
	int ret = av_opt_set_int(context, "rc_mode", 1, AV_OPT_SEARCH_CHILDREN);
	if (ret < 0) {
		lj_fferror(error, error_size, "setting VAAPI CQP mode", ret);
		return ret;
	}
	if (context->priv_data)
		av_opt_set_int(context->priv_data, "compression_level", 1, 0);
	av_opt_set_int(context, "compression_level", 1, AV_OPT_SEARCH_CHILDREN);
	return 0;
}

void lj_report_progress(LJProgressCallback callback, int percent, int *last_percent)
{
	if (!callback || !last_percent)
		return;
	if (percent < 0)
		percent = 0;
	if (percent > 100)
		percent = 100;
	int bucket = (percent / 5) * 5;
	if (percent == 100)
		bucket = 100;
	if (bucket != *last_percent) {
		callback(bucket);
		*last_percent = bucket;
	}
}

int64_t lj_stream_duration_ns(const AVStream *stream)
{
	if (!stream || stream->duration == AV_NOPTS_VALUE || stream->duration < 0)
		return 0;
	return av_rescale_q(stream->duration, stream->time_base, (AVRational){1, 1000000000});
}

int lj_alloc_vaapi_frames(AVBufferRef *device_ref, enum AVPixelFormat sw_format,
	int width, int height, int pool_size,
	AVBufferRef **frames_ref_out)
{
	if (!frames_ref_out)
		return AVERROR(EINVAL);
	*frames_ref_out = NULL;
	if (!device_ref || width <= 0 || height <= 0)
		return AVERROR(EINVAL);
	AVBufferRef *frames_ref = av_hwframe_ctx_alloc(device_ref);
	if (!frames_ref)
		return AVERROR(ENOMEM);
	AVHWFramesContext *frames = (AVHWFramesContext *)frames_ref->data;
	frames->format = AV_PIX_FMT_VAAPI;
	frames->sw_format = sw_format;
	frames->width = width;
	frames->height = height;
	frames->initial_pool_size = pool_size;
	int ret = av_hwframe_ctx_init(frames_ref);
	if (ret < 0) {
		av_buffer_unref(&frames_ref);
		return ret;
	}
	*frames_ref_out = frames_ref;
	return 0;
}
