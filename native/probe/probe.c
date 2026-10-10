#include "../config/encoder_profile.h"
#include "../hw/vaapi_device.h"

#include <libavutil/mathematics.h>
#include <stdio.h>
#include <string.h>

static int64_t lj_stream_duration_ns(const AVStream *stream)
{
	if (stream->duration == AV_NOPTS_VALUE || stream->duration < 0)
		return 0;
	return av_rescale_q(stream->duration, stream->time_base, LJ_NS_Q);
}

static int64_t lj_probe_duration_ns(const AVFormatContext *format)
{
	int64_t duration_ns = format->duration > 0
		? av_rescale_q(format->duration, AV_TIME_BASE_Q, LJ_NS_Q) : 0;
	for (unsigned int i = 0; i < format->nb_streams; ++i) {
		int64_t stream_duration = lj_stream_duration_ns(format->streams[i]);
		if (stream_duration > duration_ns)
			duration_ns = stream_duration;
	}
	return duration_ns;
}

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
		goto done;
	}
	ret = 0;

	const AVCodec *decoder = NULL;
	info->video_stream = av_find_best_stream(format, AVMEDIA_TYPE_VIDEO, -1, -1, &decoder, 0);
	if (info->video_stream >= 0) {
		const AVCodecParameters *codecpar = format->streams[info->video_stream]->codecpar;
		info->video_width = codecpar->width;
		info->video_height = codecpar->height;
		snprintf(info->video_codec, sizeof(info->video_codec), "%s",
			decoder && decoder->name ? decoder->name : "unknown");
	}
	info->audio_stream = av_find_best_stream(format, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);
	if (info->video_stream < 0 && info->audio_stream < 0) {
		lj_error(error, error_size, "input contains no audio or video stream");
		ret = AVERROR_STREAM_NOT_FOUND;
		goto done;
	}
	info->duration_ns = lj_probe_duration_ns(format);
	if (info->duration_ns <= 0) {
		lj_error(error, error_size, "input has no usable duration");
		ret = AVERROR_INVALIDDATA;
	}

done:
	avformat_close_input(&format);
	return ret;
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
	LJVideoConfig config;
	int ret = lj_video_profile_select(codec, NULL, &config, error, error_size);
	if (ret < 0)
		return ret;
	AVBufferRef *device_ref = NULL;
	ret = lj_vaapi_device_open(device, &device_ref, error, error_size);
	if (ret < 0)
		return ret;
	AVCodecContext *encoder = NULL;
	ret = lj_video_encoder_open(&config, device_ref, width, height, (AVRational){0, 0}, 0,
		&encoder, error, error_size);
	avcodec_free_context(&encoder);
	av_buffer_unref(&device_ref);
	return ret;
}
