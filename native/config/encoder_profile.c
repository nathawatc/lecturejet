#include "encoder_profile.h"

#include "../hw/vaapi_device.h"

#include <libavutil/channel_layout.h>
#include <libavutil/opt.h>
#include <string.h>

#define LJ_FALLBACK_FPS 30

static const LJVideoConfig lj_video_defaults = {
	.sw_pix_fmt = AV_PIX_FMT_NV12,
	.time_base = {1, 90000},
	.rc_mode = "CQP",
	.qp = 28,
	.gop_duration_sec = 4,
	.refs = 2,
	.max_b_frames = 0,
	.async_depth = 4,
	.compression_level = 1,
	.disable_sei = 0,
	.hw_pool_size = 16,
};

static const LJAudioConfig lj_audio_defaults = {
	.codec_id = AV_CODEC_ID_AAC,
	.sample_fmt = AV_SAMPLE_FMT_FLTP,
	.sample_rate = LJ_SAMPLE_RATE,
	.bitrate_mono = 96000,
	.bitrate_stereo = 128000,
	.default_frame_size = 1024,
};

static int lj_hevc_tag(const char *tag, uint32_t *fourcc, char *error, size_t error_size)
{
	if (!tag || !tag[0] || strcmp(tag, "hev1") == 0)
		*fourcc = MKTAG('h', 'e', 'v', '1');
	else if (strcmp(tag, "hvc1") == 0)
		*fourcc = MKTAG('h', 'v', 'c', '1');
	else {
		lj_error(error, error_size, "unsupported HEVC codec tag '%s': choose hev1 or hvc1", tag);
		return AVERROR(EINVAL);
	}
	return 0;
}

int lj_video_profile_select(const char *codec, const char *hevc_tag, LJVideoConfig *config,
	char *error, size_t error_size)
{
	*config = lj_video_defaults;
	if (codec && (strcmp(codec, "hevc") == 0 || strcmp(codec, "hevc_vaapi") == 0)) {
		config->encoder_name = "hevc_vaapi";
		config->codec_id = AV_CODEC_ID_HEVC;
		return lj_hevc_tag(hevc_tag, &config->codec_tag, error, error_size);
	}
	if (codec && (strcmp(codec, "h264") == 0 || strcmp(codec, "h264_vaapi") == 0)) {
		config->encoder_name = "h264_vaapi";
		config->codec_id = AV_CODEC_ID_H264;
		return 0;
	}
	lj_error(error, error_size, "unsupported video codec '%s': choose hevc or h264",
		codec ? codec : "");
	return AVERROR(EINVAL);
}

const LJAudioConfig *lj_audio_profile(void)
{
	return &lj_audio_defaults;
}

static int lj_video_config_valid(const LJVideoConfig *config, char *error, size_t error_size)
{
	if (config->qp < 0 || config->qp > 51 || config->gop_duration_sec < 1 ||
		config->refs < 1 || config->max_b_frames < 0 || config->async_depth < 1 ||
		config->hw_pool_size < 1) {
		lj_error(error, error_size, "invalid %s encoder profile", config->encoder_name);
		return AVERROR(EINVAL);
	}
	return 0;
}

static int lj_set_opt_int(AVCodecContext *context, const char *name, int64_t value,
	char *error, size_t error_size)
{
	int ret = av_opt_set_int(context, name, value, AV_OPT_SEARCH_CHILDREN);
	if (ret < 0)
		lj_fferror(error, error_size, name, ret);
	return ret;
}

static int lj_video_apply(AVCodecContext *context, const LJVideoConfig *config,
	int width, int height, AVRational fps, int global_header, char *error, size_t error_size)
{
	AVRational effective = fps.num > 0 && fps.den > 0 ? fps : (AVRational){LJ_FALLBACK_FPS, 1};
	int fps_int = (int)((effective.num + effective.den / 2) / effective.den);
	if (fps_int <= 0)
		fps_int = LJ_FALLBACK_FPS;
	context->width = width;
	context->height = height;
	context->time_base = config->time_base;
	context->framerate = effective;
	context->pix_fmt = AV_PIX_FMT_VAAPI;
	context->gop_size = fps_int * config->gop_duration_sec;
	context->refs = config->refs;
	context->max_b_frames = config->max_b_frames;
	context->global_quality = config->qp;
	if (global_header)
		context->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
	int ret = av_opt_set(context, "rc_mode", config->rc_mode, AV_OPT_SEARCH_CHILDREN);
	if (ret < 0) {
		lj_fferror(error, error_size, "setting VAAPI rate control mode", ret);
		return ret;
	}
	ret = lj_set_opt_int(context, "async_depth", config->async_depth, error, error_size);
	if (ret >= 0)
		ret = lj_set_opt_int(context, "compression_level", config->compression_level,
			error, error_size);
	if (ret >= 0 && config->disable_sei)
		ret = lj_set_opt_int(context, "sei", 0, error, error_size);
	return ret;
}

int lj_video_encoder_open(const LJVideoConfig *config, AVBufferRef *device_ref,
	int width, int height, AVRational fps, int global_header,
	AVCodecContext **encoder, char *error, size_t error_size)
{
	*encoder = NULL;
	int ret = lj_video_config_valid(config, error, error_size);
	if (ret < 0)
		return ret;
	const AVCodec *codec = avcodec_find_encoder_by_name(config->encoder_name);
	if (!codec) {
		lj_error(error, error_size, "finding %s encoder: encoder is unavailable",
			config->encoder_name);
		return AVERROR_ENCODER_NOT_FOUND;
	}
	AVCodecContext *context = avcodec_alloc_context3(codec);
	if (!context)
		return AVERROR(ENOMEM);
	AVBufferRef *frames_ref = NULL;
	ret = lj_video_apply(context, config, width, height, fps, global_header, error, error_size);
	if (ret >= 0)
		ret = lj_vaapi_frames_alloc(device_ref, config->sw_pix_fmt, width, height,
			config->hw_pool_size, &frames_ref);
	if (ret >= 0) {
		context->hw_device_ctx = av_buffer_ref(device_ref);
		context->hw_frames_ctx = av_buffer_ref(frames_ref);
		if (!context->hw_device_ctx || !context->hw_frames_ctx)
			ret = AVERROR(ENOMEM);
	}
	if (ret >= 0)
		ret = avcodec_open2(context, codec, NULL);
	av_buffer_unref(&frames_ref);
	if (ret < 0) {
		if (!error || error_size == 0 || !error[0])
			lj_fferror(error, error_size, "opening VAAPI encoder", ret);
		avcodec_free_context(&context);
		return ret;
	}
	*encoder = context;
	return 0;
}

int lj_audio_encoder_open(const LJAudioConfig *config, int channels, int global_header,
	AVCodecContext **encoder)
{
	*encoder = NULL;
	const AVCodec *codec = avcodec_find_encoder(config->codec_id);
	if (!codec)
		return AVERROR_ENCODER_NOT_FOUND;
	AVCodecContext *context = avcodec_alloc_context3(codec);
	if (!context)
		return AVERROR(ENOMEM);
	context->sample_fmt = config->sample_fmt;
	context->sample_rate = config->sample_rate;
	context->bit_rate = channels == 1 ? config->bitrate_mono : config->bitrate_stereo;
	context->time_base = (AVRational){1, config->sample_rate};
	av_channel_layout_default(&context->ch_layout, channels);
	if (global_header)
		context->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
	int ret = avcodec_open2(context, codec, NULL);
	if (ret < 0) {
		avcodec_free_context(&context);
		return ret;
	}
	*encoder = context;
	return 0;
}
