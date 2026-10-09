#include "internal.h"

int lj_probe_file(const char *path, LJProbeInfo *info, char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	if (!path || !info) {
		lj_error(error, error_size, "probing media: invalid argument");
		return AVERROR(EINVAL);
	}
	memset(info, 0, sizeof(*info));
	info->audio_stream = -1;
	info->video_stream = -1;

	AVFormatContext *format = NULL;
	int ret = avformat_open_input(&format, path, NULL, NULL);
	if (ret < 0) {
		lj_fferror(error, error_size, "opening input", ret);
		return ret;
	}
	ret = avformat_find_stream_info(format, NULL);
	if (ret < 0) {
		lj_fferror(error, error_size, "reading stream information", ret);
		avformat_close_input(&format);
		return ret;
	}

	const AVCodec *decoder = NULL;
	info->video_stream = av_find_best_stream(format, AVMEDIA_TYPE_VIDEO, -1, -1, &decoder, 0);
	if (info->video_stream >= 0) {
		AVStream *stream = format->streams[info->video_stream];
		info->video_width = stream->codecpar->width;
		info->video_height = stream->codecpar->height;
		snprintf(info->video_codec, sizeof(info->video_codec), "%s",
			decoder && decoder->name ? decoder->name : "unknown");
	}
	info->audio_stream = av_find_best_stream(format, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);
	if (info->video_stream < 0 && info->audio_stream < 0) {
		lj_error(error, error_size, "input contains no audio or video stream");
		avformat_close_input(&format);
		return AVERROR_STREAM_NOT_FOUND;
	}

	int64_t duration_ns = format->duration > 0
		? av_rescale_q(format->duration, AV_TIME_BASE_Q,
				(AVRational){1, 1000000000})
		: 0;
	for (unsigned int i = 0; i < format->nb_streams; ++i) {
		int64_t stream_duration = lj_stream_duration_ns(format->streams[i]);
		if (stream_duration > duration_ns)
			duration_ns = stream_duration;
	}
	info->duration_ns = duration_ns;
	avformat_close_input(&format);
	if (info->duration_ns <= 0) {
		lj_error(error, error_size, "input has no usable duration");
		return AVERROR_INVALIDDATA;
	}
	return 0;
}

int lj_probe_vaapi(const char *device, const char *codec, int width, int height,
	char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	if (!codec || width <= 0 || height <= 0) {
		lj_error(error, error_size, "probing VAAPI: invalid codec or dimensions");
		return AVERROR(EINVAL);
	}
	const AVCodec *encoder = avcodec_find_encoder_by_name(codec);
	if (!encoder) {
		lj_error(error, error_size, "finding %s encoder: encoder is unavailable", codec);
		return AVERROR_ENCODER_NOT_FOUND;
	}
	AVBufferRef *device_ref = NULL;
	int ret = av_hwdevice_ctx_create(&device_ref, AV_HWDEVICE_TYPE_VAAPI, device, NULL, 0);
	if (ret < 0) {
		lj_fferror(error, error_size, "opening VAAPI device", ret);
		return ret;
	}
	AVCodecContext *context = avcodec_alloc_context3(encoder);
	if (!context) {
		av_buffer_unref(&device_ref);
		lj_error(error, error_size, "allocating %s encoder context: out of memory", codec);
		return AVERROR(ENOMEM);
	}
	context->width = width;
	context->height = height;
	context->time_base = (AVRational){1, 90000};
	context->pix_fmt = AV_PIX_FMT_VAAPI;
	context->hw_device_ctx = av_buffer_ref(device_ref);
	AVBufferRef *frames_ref = NULL;
	if (!context->hw_device_ctx) {
		ret = AVERROR(ENOMEM);
	} else {
		ret = lj_set_video_quality(context, error, error_size);
	}
	if (ret >= 0)
		ret = lj_alloc_vaapi_frames(device_ref, AV_PIX_FMT_NV12, width, height, 4, &frames_ref);
	if (ret >= 0) {
		context->hw_frames_ctx = av_buffer_ref(frames_ref);
		if (!context->hw_frames_ctx)
			ret = AVERROR(ENOMEM);
		else
			ret = avcodec_open2(context, encoder, NULL);
	}
	if (ret < 0 && (!error || error[0] == '\0'))
		lj_fferror(error, error_size, "opening VAAPI encoder", ret);
	av_buffer_unref(&frames_ref);
	avcodec_free_context(&context);
	av_buffer_unref(&device_ref);
	return ret;
}
