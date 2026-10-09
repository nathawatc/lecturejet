#include "lecturejet_ffmpeg.h"

#include <math.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libavutil/avassert.h>
#include <libavutil/channel_layout.h>
#include <libavutil/error.h>
#include <libavutil/hwcontext.h>
#include <libavutil/imgutils.h>
#include <libavutil/mathematics.h>
#include <libavutil/opt.h>
#include <libavutil/pixdesc.h>
#include <libavutil/samplefmt.h>
#include <libswresample/swresample.h>
#include <libswscale/swscale.h>

#define LJ_DEFAULT_VIDEO_QP 28

struct LJCancelToken {
	int cancelled;
};

static void lj_error(char *error, size_t error_size, const char *format, ...)
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

static int lj_cancel_requested(const LJCancelToken *token)
{
	return token && __atomic_load_n(&token->cancelled, __ATOMIC_ACQUIRE);
}

static int lj_ffmpeg_interrupt(void *opaque)
{
	return lj_cancel_requested((const LJCancelToken *)opaque);
}

static int lj_abort_if_cancelled(LJCancelToken *token, char *error, size_t error_size)
{
	if (!lj_cancel_requested(token))
		return 0;
	lj_error(error, error_size, "operation cancelled");
	return AVERROR_EXIT;
}

static void lj_set_interrupt_callback(AVFormatContext *format, LJCancelToken *token)
{
	if (!format || !token)
		return;
	format->interrupt_callback.callback = lj_ffmpeg_interrupt;
	format->interrupt_callback.opaque = token;
}

static void lj_fferror(char *error, size_t error_size, const char *operation, int code)
{
	char detail[AV_ERROR_MAX_STRING_SIZE];
	av_strerror(code, detail, sizeof(detail));
	lj_error(error, error_size, "%s: %s", operation, detail);
}

static uint32_t lj_fourcc(const char tag[4])
{
	return (uint32_t)(unsigned char)tag[0] |
		((uint32_t)(unsigned char)tag[1] << 8) |
		((uint32_t)(unsigned char)tag[2] << 16) |
		((uint32_t)(unsigned char)tag[3] << 24);
}

static int lj_set_video_quality(AVCodecContext *context, char *error, size_t error_size)
{
	// FFmpeg VAAPI maps global_quality to CQP quality when rc_mode is CQP.
	context->global_quality = LJ_DEFAULT_VIDEO_QP;
	// h264_vaapi and hevc_vaapi expose CQP as rc_mode value 1.
	int ret = av_opt_set_int(context, "rc_mode", 1, AV_OPT_SEARCH_CHILDREN);
	if (ret < 0) {
		lj_fferror(error, error_size, "setting VAAPI CQP mode", ret);
		return ret;
	}
	return ret;
}

static void lj_report_progress(LJProgressCallback callback, int percent, int *last_percent)
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

static int64_t lj_stream_duration_ns(const AVStream *stream)
{
	if (!stream || stream->duration == AV_NOPTS_VALUE || stream->duration < 0)
		return 0;
	return av_rescale_q(stream->duration, stream->time_base, (AVRational){1, 1000000000});
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

static int lj_alloc_vaapi_frames(AVBufferRef *device_ref, enum AVPixelFormat sw_format,
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

struct LJAudioDecoder {
	AVFormatContext *format;
	AVCodecContext *codec;
	LJCancelToken *cancel;
	SwrContext *resampler;
	AVPacket *packet;
	AVFrame *frame;
	int stream_index;
	int output_channels;
	int input_eof;
	int decoder_flush_sent;
	int decoder_eof;
	int swr_flushed;
	int filter_eof_sent;
	int filter_eof;
	int64_t emitted_samples;
	int64_t filter_input_samples;
	int64_t filter_target_samples;

	AVFilterGraph *filter_graph;
	AVFilterContext *filter_source;
	AVFilterContext *filter_sink;

	float *pending;
	size_t pending_frames;
	size_t pending_capacity;
	char error[512];
};

static int lj_audio_append(LJAudioDecoder *decoder, const float *samples, size_t frames,
	int channels)
{
	if (frames == 0)
		return 0;
	if (channels <= 0 || channels > 2)
		return AVERROR(EINVAL);
	if (frames > SIZE_MAX / (size_t)decoder->output_channels ||
		decoder->pending_frames > SIZE_MAX / (size_t)decoder->output_channels - frames)
		return AVERROR(ENOMEM);
	size_t required = decoder->pending_frames + frames;
	if (required > decoder->pending_capacity) {
		size_t capacity = decoder->pending_capacity ? decoder->pending_capacity : 4096;
		while (capacity < required) {
			if (capacity > SIZE_MAX / 2)
				return AVERROR(ENOMEM);
			capacity *= 2;
		}
		if (capacity > SIZE_MAX / (size_t)decoder->output_channels ||
			capacity * (size_t)decoder->output_channels > SIZE_MAX / sizeof(float))
			return AVERROR(ENOMEM);
		float *grown = realloc(decoder->pending,
			capacity * (size_t)decoder->output_channels * sizeof(float));
		if (!grown)
			return AVERROR(ENOMEM);
		decoder->pending = grown;
		decoder->pending_capacity = capacity;
	}
	float *destination = decoder->pending + decoder->pending_frames * decoder->output_channels;
	if (channels == decoder->output_channels) {
		memcpy(destination, samples, frames * (size_t)channels * sizeof(float));
	} else if (channels == 1 && decoder->output_channels == 2) {
		for (size_t i = 0; i < frames; ++i) {
			destination[i * 2] = samples[i];
			destination[i * 2 + 1] = samples[i];
		}
	} else {
		return AVERROR(EINVAL);
	}
	decoder->pending_frames += frames;
	return 0;
}

static int lj_audio_filter_drain(LJAudioDecoder *decoder)
{
	if (!decoder->filter_sink || decoder->filter_eof)
		return 1;
	AVFrame *filtered = av_frame_alloc();
	if (!filtered)
		return AVERROR(ENOMEM);
	int result = 0;
	int ret = av_buffersink_get_samples(decoder->filter_sink, filtered, 480);
	if (ret == AVERROR(EAGAIN)) {
		result = 1;
		goto cleanup;
	}
	if (ret == AVERROR_EOF) {
		decoder->filter_eof = 1;
		result = 1;
		goto cleanup;
	}
	if (ret < 0) {
		lj_fferror(decoder->error, sizeof(decoder->error), "reading RNNoise output", ret);
		result = ret;
		goto cleanup;
	}
	int valid_samples = filtered->nb_samples;
	if (decoder->filter_target_samples > 0) {
		int64_t remaining = decoder->filter_target_samples - decoder->emitted_samples -
			(int64_t)decoder->pending_frames;
		if (remaining <= 0)
			valid_samples = 0;
		else if (remaining < valid_samples)
			valid_samples = (int)remaining;
	}
	const float *filtered_samples = (const float *)filtered->data[0];
	ret = lj_audio_append(decoder, filtered_samples, valid_samples, 1);
	if (ret < 0) {
		lj_fferror(decoder->error, sizeof(decoder->error), "buffering RNNoise output", ret);
		result = ret;
		goto cleanup;
	}
	result = 0;

cleanup:
	av_frame_free(&filtered);
	return result;
}

static int lj_audio_filter_feed(LJAudioDecoder *decoder, const float *samples, int frames)
{
	AVFrame *input = av_frame_alloc();
	if (!input)
		return AVERROR(ENOMEM);
	input->format = AV_SAMPLE_FMT_FLT;
	input->sample_rate = 48000;
	input->nb_samples = frames;
	input->pts = decoder->filter_input_samples;
	av_channel_layout_default(&input->ch_layout, 1);
	int ret = av_frame_get_buffer(input, 0);
	if (ret >= 0)
		memcpy(input->data[0], samples, (size_t)frames * sizeof(float));
	if (ret >= 0)
		ret = av_buffersrc_add_frame_flags(decoder->filter_source, input, AV_BUFFERSRC_FLAG_KEEP_REF);
	if (ret >= 0)
		decoder->filter_input_samples += frames;
	av_frame_free(&input);
	return ret;
}

static int lj_audio_filter_end(LJAudioDecoder *decoder)
{
	if (!decoder->filter_source || decoder->filter_eof_sent)
		return 0;
	int ret = av_buffersrc_close(decoder->filter_source, decoder->filter_input_samples, 0);
	if (ret < 0)
		return ret;
	decoder->filter_target_samples = decoder->filter_input_samples;
	decoder->filter_eof_sent = 1;
	return 0;
}

static int lj_audio_process_swr(LJAudioDecoder *decoder, const uint8_t **input,
	int input_samples)
{
	if (lj_cancel_requested(decoder->cancel))
		return AVERROR_EXIT;
	int output_capacity = swr_get_out_samples(decoder->resampler, input_samples);
	if (output_capacity < 1)
		output_capacity = input_samples + 64;
	if (output_capacity < 1)
		return 0;
	float *converted = malloc((size_t)output_capacity * decoder->output_channels * sizeof(float));
	if (!converted)
		return AVERROR(ENOMEM);
	uint8_t *output_data[1] = {(uint8_t *)converted};
	int output_samples = swr_convert(decoder->resampler, output_data, output_capacity,
		input, input_samples);
	int ret = output_samples;
	if (output_samples < 0)
		goto cleanup;
	if (decoder->filter_source) {
		// RNNoise receives mono. The resampler is configured accordingly.
		ret = lj_audio_filter_feed(decoder, converted, output_samples);
		if (ret < 0)
			goto cleanup;
		int drain;
		do {
			drain = lj_audio_filter_drain(decoder);
			if (drain < 0) {
				ret = drain;
				goto cleanup;
			}
		} while (drain == 0);
		ret = 0;
	} else {
		ret = lj_audio_append(decoder, converted, output_samples, decoder->output_channels);
	}

cleanup:
	free(converted);
	return ret;
}

static int lj_audio_flush_swr(LJAudioDecoder *decoder)
{
	if (decoder->swr_flushed)
		return 0;
	int ret = 0;
	float *converted = NULL;
	for (;;) {
		if (lj_cancel_requested(decoder->cancel)) {
			ret = AVERROR_EXIT;
			goto cleanup;
		}
		int output_capacity = swr_get_out_samples(decoder->resampler, 0);
		if (output_capacity <= 0)
			break;
		if (output_capacity > 8192)
			output_capacity = 8192;
		converted = malloc((size_t)output_capacity * decoder->output_channels * sizeof(float));
		if (!converted) {
			ret = AVERROR(ENOMEM);
			goto cleanup;
		}
		uint8_t *output_data[1] = {(uint8_t *)converted};
		int output_samples = swr_convert(decoder->resampler, output_data, output_capacity, NULL, 0);
		if (output_samples < 0) {
			ret = output_samples;
			goto cleanup;
		}
		if (output_samples == 0) {
			free(converted);
			converted = NULL;
			break;
		}
		if (decoder->filter_source) {
			ret = lj_audio_filter_feed(decoder, converted, output_samples);
			if (ret < 0)
				goto cleanup;
			int drain;
			do {
				drain = lj_audio_filter_drain(decoder);
				if (drain < 0) {
					ret = drain;
					goto cleanup;
				}
			} while (drain == 0);
		} else {
			ret = lj_audio_append(decoder, converted, output_samples,
				decoder->output_channels);
			if (ret < 0)
				goto cleanup;
		}
		free(converted);
		converted = NULL;
	}
	decoder->swr_flushed = 1;
	ret = 0;

cleanup:
	free(converted);
	return ret;
}

static int lj_audio_decode_more(LJAudioDecoder *decoder)
{
	for (;;) {
		if (lj_cancel_requested(decoder->cancel)) {
			lj_error(decoder->error, sizeof(decoder->error), "operation cancelled");
			return AVERROR_EXIT;
		}
		int ret = avcodec_receive_frame(decoder->codec, decoder->frame);
		if (ret == 0) {
			ret = lj_audio_process_swr(decoder,
				(const uint8_t **)decoder->frame->extended_data,
				decoder->frame->nb_samples);
			av_frame_unref(decoder->frame);
			if (ret < 0)
				return ret;
			if (decoder->pending_frames > 0)
				return 0;
			continue;
		}
		if (ret == AVERROR_EOF) {
			decoder->decoder_eof = 1;
			ret = lj_audio_flush_swr(decoder);
			if (ret < 0)
				return ret;
			if (decoder->filter_source) {
				ret = lj_audio_filter_end(decoder);
				if (ret < 0)
					return ret;
				int drain;
				do {
					drain = lj_audio_filter_drain(decoder);
					if (drain < 0)
						return drain;
				} while (drain == 0);
			}
			return 0;
		}
		if (ret != AVERROR(EAGAIN))
			return ret;

		if (!decoder->input_eof) {
			ret = av_read_frame(decoder->format, decoder->packet);
			if (ret == AVERROR_EOF) {
				decoder->input_eof = 1;
			} else if (ret < 0) {
				if (lj_cancel_requested(decoder->cancel))
					lj_error(decoder->error, sizeof(decoder->error), "operation cancelled");
				return ret;
			} else {
				if (decoder->packet->stream_index == decoder->stream_index) {
					ret = avcodec_send_packet(decoder->codec, decoder->packet);
					av_packet_unref(decoder->packet);
					if (ret < 0)
						return ret;
				} else {
					av_packet_unref(decoder->packet);
				}
				continue;
			}
		}
		if (!decoder->decoder_flush_sent) {
			ret = avcodec_send_packet(decoder->codec, NULL);
			if (ret < 0 && ret != AVERROR_EOF)
				return ret;
			decoder->decoder_flush_sent = 1;
			continue;
		}
		return AVERROR_BUG;
	}
}

LJAudioDecoder *lj_audio_open(const char *path, int output_channels,
	LJCancelToken *cancel,
	int rnnoise_enabled, double rnnoise_strength,
	const char *rnnoise_model, char *error,
	size_t error_size)
{
	if (output_channels != 1 && output_channels != 2) {
		lj_error(error, error_size, "opening audio decoder: output channels must be 1 or 2");
		return NULL;
	}
	if (rnnoise_enabled && (!rnnoise_model || !*rnnoise_model)) {
		lj_error(error, error_size, "opening RNNoise: model path is required");
		return NULL;
	}
	LJAudioDecoder *decoder = calloc(1, sizeof(*decoder));
	if (!decoder) {
		lj_error(error, error_size, "allocating audio decoder: out of memory");
		return NULL;
	}
	decoder->output_channels = output_channels;
	decoder->cancel = cancel;
	int ret = 0;
	decoder->format = avformat_alloc_context();
	if (!decoder->format) {
		ret = AVERROR(ENOMEM);
		goto fail;
	}
	lj_set_interrupt_callback(decoder->format, decoder->cancel);
	ret = avformat_open_input(&decoder->format, path, NULL, NULL);
	if (ret < 0)
		goto fail;
	ret = avformat_find_stream_info(decoder->format, NULL);
	if (ret < 0)
		goto fail;
	const AVCodec *codec = NULL;
	decoder->stream_index = av_find_best_stream(decoder->format, AVMEDIA_TYPE_AUDIO,
		-1, -1, &codec, 0);
	if (decoder->stream_index < 0) {
		ret = AVERROR_STREAM_NOT_FOUND;
		goto fail;
	}
	decoder->codec = avcodec_alloc_context3(codec);
	if (!decoder->codec) {
		ret = AVERROR(ENOMEM);
		goto fail;
	}
	ret = avcodec_parameters_to_context(decoder->codec,
		decoder->format->streams[decoder->stream_index]->codecpar);
	if (ret < 0)
		goto fail;
	decoder->codec->pkt_timebase = decoder->format->streams[decoder->stream_index]->time_base;
	decoder->codec->thread_count = 1;
	ret = avcodec_open2(decoder->codec, codec, NULL);
	if (ret < 0)
		goto fail;
	decoder->packet = av_packet_alloc();
	decoder->frame = av_frame_alloc();
	if (!decoder->packet || !decoder->frame) {
		ret = AVERROR(ENOMEM);
		goto fail;
	}

	AVChannelLayout input_layout = decoder->codec->ch_layout;
	if (input_layout.nb_channels <= 0) {
		av_channel_layout_default(&input_layout, decoder->codec->ch_layout.nb_channels > 0
			? decoder->codec->ch_layout.nb_channels
			: 1);
	}
	AVChannelLayout output_layout;
	av_channel_layout_default(&output_layout, rnnoise_enabled ? 1 : output_channels);
	ret = swr_alloc_set_opts2(&decoder->resampler, &output_layout, AV_SAMPLE_FMT_FLT, 48000,
		&input_layout, decoder->codec->sample_fmt,
		decoder->codec->sample_rate, 0, NULL);
	if (ret < 0)
		goto fail;
	ret = swr_init(decoder->resampler);
	if (ret < 0)
		goto fail;

	if (rnnoise_enabled) {
		decoder->filter_graph = avfilter_graph_alloc();
		if (!decoder->filter_graph) {
			ret = AVERROR(ENOMEM);
			goto fail;
		}
		const AVFilter *buffer_source = avfilter_get_by_name("abuffer");
		const AVFilter *buffer_sink = avfilter_get_by_name("abuffersink");
		const AVFilter *sample_frames = avfilter_get_by_name("asetnsamples");
		const AVFilter *rnnoise = avfilter_get_by_name("arnndn");
		if (!buffer_source || !buffer_sink || !sample_frames || !rnnoise) {
			ret = AVERROR_FILTER_NOT_FOUND;
			goto fail;
		}
		ret = avfilter_graph_create_filter(&decoder->filter_source, buffer_source, "input",
			"time_base=1/48000:sample_rate=48000:sample_fmt=flt:channel_layout=mono",
			NULL, decoder->filter_graph);
		if (ret < 0)
			goto fail;
		AVFilterContext *noise = avfilter_graph_alloc_filter(decoder->filter_graph, rnnoise,
			"rnnoise");
		ret = noise ? 0 : AVERROR(ENOMEM);
		if (ret >= 0)
			ret = av_opt_set(noise, "model", rnnoise_model, AV_OPT_SEARCH_CHILDREN);
		if (ret >= 0)
			ret = av_opt_set_double(noise, "mix", rnnoise_strength, AV_OPT_SEARCH_CHILDREN);
		if (ret >= 0)
			ret = avfilter_init_str(noise, NULL);
		AVFilterContext *sample_frame_filter = NULL;
		if (ret >= 0)
			ret = avfilter_graph_create_filter(&sample_frame_filter, sample_frames, "frames",
				"n=480:pad=1", NULL, decoder->filter_graph);
		if (ret >= 0)
			ret = avfilter_graph_create_filter(&decoder->filter_sink, buffer_sink, "output",
				NULL, NULL, decoder->filter_graph);
		if (ret >= 0)
			ret = avfilter_link(decoder->filter_source, 0, sample_frame_filter, 0);
		if (ret >= 0)
			ret = avfilter_link(sample_frame_filter, 0, noise, 0);
		if (ret >= 0)
			ret = avfilter_link(noise, 0, decoder->filter_sink, 0);
		if (ret >= 0)
			ret = avfilter_graph_config(decoder->filter_graph, NULL);
		if (ret < 0)
			goto fail;
	}
	return decoder;

fail:
	if (ret < 0) {
		if (ret == AVERROR_EXIT && lj_cancel_requested(decoder->cancel))
			lj_error(decoder->error, sizeof(decoder->error), "operation cancelled");
		else
			lj_fferror(decoder->error, sizeof(decoder->error), "opening audio decoder", ret);
	}
	if (decoder->error[0])
		lj_error(error, error_size, "%s", decoder->error);
	else
		lj_error(error, error_size, "opening audio decoder failed");
	lj_audio_close(&decoder);
	return NULL;
}

int lj_audio_read(LJAudioDecoder *decoder, float *output, int max_samples,
	int64_t *start_sample, char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	if (!decoder || !output || max_samples <= 0) {
		lj_error(error, error_size, "reading audio: invalid argument");
		return -1;
	}
	while (decoder->pending_frames < (size_t)max_samples && !decoder->decoder_eof) {
		int ret = lj_audio_decode_more(decoder);
		if (ret < 0) {
			if (lj_cancel_requested(decoder->cancel))
				lj_error(error, error_size, "operation cancelled");
			else if (decoder->error[0])
				lj_error(error, error_size, "%s", decoder->error);
			else
				lj_fferror(error, error_size, "decoding audio", ret);
			return -1;
		}
		if (decoder->decoder_eof)
			break;
	}
	if (decoder->pending_frames == 0) {
		if (start_sample)
			*start_sample = decoder->emitted_samples;
		return 0;
	}
	size_t frames = decoder->pending_frames < (size_t)max_samples
		? decoder->pending_frames
		: (size_t)max_samples;
	size_t values = frames * (size_t)decoder->output_channels;
	for (size_t i = 0; i < values; ++i) {
		if (!isfinite(decoder->pending[i])) {
			lj_error(error, error_size, "decoding audio: non-finite PCM sample at index %zu", i);
			return -1;
		}
	}
	if (start_sample)
		*start_sample = decoder->emitted_samples;
	memcpy(output, decoder->pending, values * sizeof(float));
	decoder->pending_frames -= frames;
	memmove(decoder->pending, decoder->pending + values,
		decoder->pending_frames * (size_t)decoder->output_channels * sizeof(float));
	decoder->emitted_samples += (int64_t)frames;
	return (int)frames;
}

void lj_audio_close(LJAudioDecoder **decoder_ptr)
{
	if (!decoder_ptr || !*decoder_ptr)
		return;
	LJAudioDecoder *decoder = *decoder_ptr;
	avfilter_graph_free(&decoder->filter_graph);
	swr_free(&decoder->resampler);
	av_frame_free(&decoder->frame);
	av_packet_free(&decoder->packet);
	avcodec_free_context(&decoder->codec);
	avformat_close_input(&decoder->format);
	free(decoder->pending);
	free(decoder);
	*decoder_ptr = NULL;
}

typedef struct LJTimelineView {
	const LJSegment *segments;
	size_t count;
	int64_t *output_start;
	int64_t *sample_start;
	int64_t *sample_end;
	int64_t source_end;
	size_t cursor;
} LJTimelineView;

static void lj_timeline_free(LJTimelineView *view);

static int lj_timeline_init(LJTimelineView *view, const LJSegment *segments, size_t count)
{
	if (!view || !segments || count == 0)
		return AVERROR(EINVAL);
	memset(view, 0, sizeof(*view));
	view->segments = segments;
	view->count = count;
	view->output_start = calloc(count, sizeof(*view->output_start));
	view->sample_start = calloc(count, sizeof(*view->sample_start));
	view->sample_end = calloc(count, sizeof(*view->sample_end));
	if (!view->output_start || !view->sample_start || !view->sample_end) {
		lj_timeline_free(view);
		return AVERROR(ENOMEM);
	}
	int64_t position = 0;
	int64_t output = 0;
	for (size_t i = 0; i < count; ++i) {
		const LJSegment *segment = &segments[i];
		if (segment->start_ns != position || segment->end_ns <= segment->start_ns ||
			(segment->action != 0 && segment->action != 1)) {
			lj_timeline_free(view);
			return AVERROR_INVALIDDATA;
		}
		view->output_start[i] = output;
		view->sample_start[i] = av_rescale_q_rnd(segment->start_ns, (AVRational){1, 1000000000},
			(AVRational){1, 48000}, AV_ROUND_UP);
		view->sample_end[i] = av_rescale_q_rnd(segment->end_ns, (AVRational){1, 1000000000},
			(AVRational){1, 48000}, AV_ROUND_UP);
		position = segment->end_ns;
		if (segment->action == 0)
			output += segment->end_ns - segment->start_ns;
	}
	view->source_end = position;
	return 0;
}

static void lj_timeline_free(LJTimelineView *view)
{
	if (!view)
		return;
	free(view->output_start);
	free(view->sample_start);
	free(view->sample_end);
	memset(view, 0, sizeof(*view));
}

static int lj_timeline_lookup_ns(LJTimelineView *view, int64_t source_ns, size_t *index)
{
	if (!view || source_ns < 0 || source_ns >= view->source_end)
		return 0;
	while (view->cursor > 0 && source_ns < view->segments[view->cursor].start_ns)
		--view->cursor;
	while (view->cursor + 1 < view->count && source_ns >= view->segments[view->cursor].end_ns)
		++view->cursor;
	if (source_ns < view->segments[view->cursor].start_ns ||
		source_ns >= view->segments[view->cursor].end_ns)
		return 0;
	if (index)
		*index = view->cursor;
	return 1;
}

static int lj_timeline_lookup_sample(LJTimelineView *view, int64_t sample, size_t *index)
{
	if (!view || sample < 0 || sample >= view->sample_end[view->count - 1])
		return 0;
	while (view->cursor > 0 && sample < view->sample_start[view->cursor])
		--view->cursor;
	while (view->cursor + 1 < view->count && sample >= view->sample_end[view->cursor])
		++view->cursor;
	if (sample < view->sample_start[view->cursor] || sample >= view->sample_end[view->cursor])
		return 0;
	if (index)
		*index = view->cursor;
	return 1;
}

static int64_t lj_timeline_map_ns(const LJTimelineView *view, size_t index, int64_t source_ns)
{
	const LJSegment *segment = &view->segments[index];
	if (segment->action == 1)
		return view->output_start[index];
	return view->output_start[index] + source_ns - segment->start_ns;
}

typedef struct LJRenderState {
	AVFormatContext *output;
	LJCancelToken *cancel;
	AVStream *video_stream;
	AVStream *audio_stream;
	AVCodecContext *video_encoder;
	AVCodecContext *audio_encoder;
	AVBufferRef *vaapi_device;
	AVBufferRef *hw_frames;
	SwsContext *scaler;
	SwrContext *audio_resampler;
	int audio_channels;
	int audio_frame_size;
	float *audio_fifo;
	size_t audio_fifo_frames;
	size_t audio_fifo_capacity;
	int64_t audio_written;
	int64_t last_video_pts;
	char error[512];
} LJRenderState;

static int lj_write_packet(LJRenderState *state, AVPacket *packet, AVStream *stream,
	AVRational source_time_base)
{
	int ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
	if (ret < 0)
		return ret;
	packet->stream_index = stream->index;
	av_packet_rescale_ts(packet, source_time_base, stream->time_base);
	if (packet->dts != AV_NOPTS_VALUE && packet->dts < 0)
		packet->dts = 0;
	if (packet->pts != AV_NOPTS_VALUE && packet->pts < 0)
		packet->pts = 0;
	ret = av_interleaved_write_frame(state->output, packet);
	if (ret < 0)
		lj_fferror(state->error, sizeof(state->error), "writing output packet", ret);
	return ret;
}

static int lj_drain_audio(LJRenderState *state, int final_valid_samples)
{
	AVPacket *packet = av_packet_alloc();
	if (!packet)
		return AVERROR(ENOMEM);
	for (;;) {
		int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error,
			sizeof(state->error));
		if (cancel_ret < 0) {
			av_packet_free(&packet);
			return cancel_ret;
		}
		int ret = avcodec_receive_packet(state->audio_encoder, packet);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
			av_packet_free(&packet);
			return 0;
		}
		if (ret < 0) {
			av_packet_free(&packet);
			return ret;
		}
		if (final_valid_samples > 0 && final_valid_samples < state->audio_frame_size)
			packet->duration = final_valid_samples;
		ret = lj_write_packet(state, packet, state->audio_stream,
			state->audio_encoder->time_base);
		av_packet_unref(packet);
		if (ret < 0) {
			av_packet_free(&packet);
			return ret;
		}
	}
}

static int lj_audio_fifo_append(LJRenderState *state, const float *samples, size_t frames)
{
	if (frames == 0)
		return 0;
	size_t required = state->audio_fifo_frames + frames;
	if (required < state->audio_fifo_frames)
		return AVERROR(ENOMEM);
	if (required > state->audio_fifo_capacity) {
		size_t capacity = state->audio_fifo_capacity ? state->audio_fifo_capacity : 4096;
		while (capacity < required) {
			if (capacity > SIZE_MAX / 2)
				return AVERROR(ENOMEM);
			capacity *= 2;
		}
		if (capacity > SIZE_MAX / (size_t)state->audio_channels ||
			capacity * (size_t)state->audio_channels > SIZE_MAX / sizeof(float))
			return AVERROR(ENOMEM);
		float *grown = realloc(state->audio_fifo,
			capacity * (size_t)state->audio_channels * sizeof(float));
		if (!grown)
			return AVERROR(ENOMEM);
		state->audio_fifo = grown;
		state->audio_fifo_capacity = capacity;
	}
	memcpy(state->audio_fifo + state->audio_fifo_frames * state->audio_channels, samples,
		frames * (size_t)state->audio_channels * sizeof(float));
	state->audio_fifo_frames += frames;
	return 0;
}

static int lj_encode_audio_available(LJRenderState *state, int flush_partial)
{
	while (state->audio_fifo_frames >= (size_t)state->audio_frame_size ||
		(flush_partial && state->audio_fifo_frames > 0)) {
		int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error,
			sizeof(state->error));
		if (cancel_ret < 0)
			return cancel_ret;
		size_t valid = state->audio_fifo_frames < (size_t)state->audio_frame_size
			? state->audio_fifo_frames
			: (size_t)state->audio_frame_size;
		AVFrame *frame = av_frame_alloc();
		if (!frame)
			return AVERROR(ENOMEM);
		frame->nb_samples = state->audio_frame_size;
		frame->format = state->audio_encoder->sample_fmt;
		frame->sample_rate = state->audio_encoder->sample_rate;
		if (av_channel_layout_copy(&frame->ch_layout, &state->audio_encoder->ch_layout) < 0) {
			av_frame_free(&frame);
			return AVERROR(ENOMEM);
		}
		int ret = av_frame_get_buffer(frame, 0);
		if (ret < 0) {
			av_frame_free(&frame);
			return ret;
		}
		size_t input_values = (size_t)state->audio_frame_size * state->audio_channels;
		float *input = calloc(input_values, sizeof(float));
		if (!input) {
			av_frame_free(&frame);
			return AVERROR(ENOMEM);
		}
		memcpy(input, state->audio_fifo, valid * (size_t)state->audio_channels * sizeof(float));
		const uint8_t *input_data[1] = {(const uint8_t *)input};
		ret = swr_convert(state->audio_resampler, frame->data, state->audio_frame_size,
			input_data, state->audio_frame_size);
		free(input);
		if (ret < 0) {
			av_frame_free(&frame);
			return ret;
		}
		frame->pts = state->audio_written;
		ret = avcodec_send_frame(state->audio_encoder, frame);
		av_frame_free(&frame);
		if (ret < 0) {
			if (ret == AVERROR(EAGAIN))
				return AVERROR_BUG;
			return ret;
		}
		ret = lj_drain_audio(state, valid < (size_t)state->audio_frame_size ? (int)valid : 0);
		if (ret < 0)
			return ret;
		state->audio_written += (int64_t)valid;
		state->audio_fifo_frames -= valid;
		memmove(state->audio_fifo,
			state->audio_fifo + valid * (size_t)state->audio_channels,
			state->audio_fifo_frames * (size_t)state->audio_channels * sizeof(float));
		if (valid < (size_t)state->audio_frame_size)
			break;
	}
	return 0;
}

static int lj_drain_audio_encoder(LJRenderState *state)
{
	int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error,
		sizeof(state->error));
	if (cancel_ret < 0)
		return cancel_ret;
	int ret = avcodec_send_frame(state->audio_encoder, NULL);
	if (ret < 0 && ret != AVERROR_EOF)
		return ret;
	return lj_drain_audio(state, 0);
}

static int lj_drain_video(LJRenderState *state)
{
	AVPacket *packet = av_packet_alloc();
	if (!packet)
		return AVERROR(ENOMEM);
	for (;;) {
		int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error,
			sizeof(state->error));
		if (cancel_ret < 0) {
			av_packet_free(&packet);
			return cancel_ret;
		}
		int ret = avcodec_receive_packet(state->video_encoder, packet);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
			av_packet_free(&packet);
			return 0;
		}
		if (ret < 0) {
			av_packet_free(&packet);
			return ret;
		}
		ret = lj_write_packet(state, packet, state->video_stream,
			state->video_encoder->time_base);
		av_packet_unref(packet);
		if (ret < 0) {
			av_packet_free(&packet);
			return ret;
		}
	}
}

static int lj_open_audio_encoder(LJRenderState *state, int channels)
{
	const AVCodec *codec = avcodec_find_encoder(AV_CODEC_ID_AAC);
	if (!codec)
		return AVERROR_ENCODER_NOT_FOUND;
	state->audio_encoder = avcodec_alloc_context3(codec);
	if (!state->audio_encoder)
		return AVERROR(ENOMEM);
	state->audio_channels = channels;
	state->audio_encoder->sample_fmt = AV_SAMPLE_FMT_FLTP;
	state->audio_encoder->sample_rate = 48000;
	state->audio_encoder->bit_rate = channels == 1 ? 96000 : 128000;
	state->audio_encoder->time_base = (AVRational){1, 48000};
	av_channel_layout_default(&state->audio_encoder->ch_layout, channels);
	if (state->output->oformat->flags & AVFMT_GLOBALHEADER)
		state->audio_encoder->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
	int ret = avcodec_open2(state->audio_encoder, codec, NULL);
	if (ret < 0)
		return ret;
	state->audio_frame_size = state->audio_encoder->frame_size > 0
		? state->audio_encoder->frame_size
		: 1024;
	state->audio_stream = avformat_new_stream(state->output, NULL);
	if (!state->audio_stream)
		return AVERROR(ENOMEM);
	ret = avcodec_parameters_from_context(state->audio_stream->codecpar,
		state->audio_encoder);
	if (ret < 0)
		return ret;
	state->audio_stream->time_base = state->audio_encoder->time_base;
	AVChannelLayout input_layout;
	av_channel_layout_default(&input_layout, channels);
	AVChannelLayout output_layout;
	av_channel_layout_copy(&output_layout, &state->audio_encoder->ch_layout);
	ret = swr_alloc_set_opts2(&state->audio_resampler, &output_layout,
		state->audio_encoder->sample_fmt, 48000, &input_layout,
		AV_SAMPLE_FMT_FLT, 48000, 0, NULL);
	av_channel_layout_uninit(&output_layout);
	if (ret < 0)
		return ret;
	return swr_init(state->audio_resampler);
}

static int lj_open_video_encoder(LJRenderState *state, const LJRenderOptions *options,
	int width, int height, AVRational fps)
{
	const char *codec_name = options->video_codec;
	const AVCodec *codec = avcodec_find_encoder_by_name(codec_name);
	if (!codec)
		return AVERROR_ENCODER_NOT_FOUND;
	int ret = av_hwdevice_ctx_create(&state->vaapi_device, AV_HWDEVICE_TYPE_VAAPI,
		options->vaapi_device, NULL, 0);
	if (ret < 0)
		return ret;
	state->video_encoder = avcodec_alloc_context3(codec);
	if (!state->video_encoder)
		return AVERROR(ENOMEM);
	state->video_encoder->width = width;
	state->video_encoder->height = height;
	state->video_encoder->time_base = (AVRational){1, 90000};
	state->video_encoder->framerate = fps.num > 0 && fps.den > 0 ? fps : (AVRational){30, 1};
	state->video_encoder->pix_fmt = AV_PIX_FMT_VAAPI;
	state->video_encoder->gop_size = 60;
	state->video_encoder->max_b_frames = 0;
	state->video_encoder->hw_device_ctx = av_buffer_ref(state->vaapi_device);
	if (!state->video_encoder->hw_device_ctx)
		return AVERROR(ENOMEM);
	ret = lj_set_video_quality(state->video_encoder, state->error, sizeof(state->error));
	if (ret < 0)
		return ret;
	if (state->output->oformat->flags & AVFMT_GLOBALHEADER)
		state->video_encoder->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
	ret = lj_alloc_vaapi_frames(state->vaapi_device, AV_PIX_FMT_NV12, width, height, 16,
		&state->hw_frames);
	if (ret < 0)
		return ret;
	av_buffer_unref(&state->video_encoder->hw_frames_ctx);
	state->video_encoder->hw_frames_ctx = av_buffer_ref(state->hw_frames);
	if (!state->video_encoder->hw_frames_ctx)
		return AVERROR(ENOMEM);
	ret = avcodec_open2(state->video_encoder, codec, NULL);
	if (ret < 0)
		return ret;
	state->video_stream = avformat_new_stream(state->output, NULL);
	if (!state->video_stream)
		return AVERROR(ENOMEM);
	ret = avcodec_parameters_from_context(state->video_stream->codecpar,
		state->video_encoder);
	if (ret < 0)
		return ret;
	if (state->video_encoder->codec_id == AV_CODEC_ID_HEVC) {
		const char *tag = options->video_tag;
		if (!tag || tag[0] == '\0')
			tag = "hev1";
		if (strcmp(tag, "hev1") != 0 && strcmp(tag, "hvc1") != 0) {
			lj_error(state->error, sizeof(state->error),
				"unsupported HEVC codec tag '%s': choose hev1 or hvc1", tag);
			return AVERROR(EINVAL);
		}
		// HEVC VAAPI output is finalized as hvc1 only by the stream-copy remux.
		state->video_stream->codecpar->codec_tag = lj_fourcc("hev1");
	}
	state->video_stream->time_base = state->video_encoder->time_base;
	return 0;
}

static int lj_encode_video_frame(LJRenderState *state, AVFrame *decoded,
	int64_t output_pts)
{
	int ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
	if (ret < 0)
		return ret;
	if (!state->scaler) {
		state->scaler = sws_getContext(decoded->width, decoded->height,
			(enum AVPixelFormat)decoded->format,
			state->video_encoder->width,
			state->video_encoder->height,
			AV_PIX_FMT_NV12, SWS_BILINEAR, NULL, NULL, NULL);
		if (!state->scaler)
			return AVERROR(EINVAL);
	}
	AVFrame *software = av_frame_alloc();
	AVFrame *hardware = av_frame_alloc();
	if (!software || !hardware) {
		av_frame_free(&software);
		av_frame_free(&hardware);
		return AVERROR(ENOMEM);
	}
	software->format = AV_PIX_FMT_NV12;
	software->width = state->video_encoder->width;
	software->height = state->video_encoder->height;
	ret = av_frame_get_buffer(software, 32);
	if (ret >= 0)
		ret = sws_scale(state->scaler, (const uint8_t *const *)decoded->data,
					decoded->linesize, 0, decoded->height, software->data,
					software->linesize) > 0
			? 0
			: AVERROR(EINVAL);
	if (ret >= 0)
		ret = av_hwframe_get_buffer(state->hw_frames, hardware, 0);
	if (ret >= 0)
		ret = av_hwframe_transfer_data(hardware, software, 0);
	if (ret >= 0) {
		hardware->pts = output_pts;
		ret = avcodec_send_frame(state->video_encoder, hardware);
	}
	av_frame_free(&software);
	av_frame_free(&hardware);
	if (ret < 0)
		return ret;
	return lj_drain_video(state);
}

static int lj_render_audio(LJRenderState *state, const char *input,
	const LJRenderOptions *options, LJTimelineView *timeline,
	LJProgressCallback progress, int *last_progress)
{
	int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
	if (cancel_ret < 0)
		return cancel_ret;
	LJAudioDecoder *decoder = lj_audio_open(input, options->output_mono ? 1 : 2,
		state->cancel,
		options->rnnoise_enabled,
		options->rnnoise_strength,
		options->rnnoise_model, state->error,
		sizeof(state->error));
	if (!decoder)
		return AVERROR_EXTERNAL;
	int channels = options->output_mono ? 1 : 2;
	float *samples = malloc((size_t)8192 * channels * sizeof(float));
	if (!samples) {
		lj_audio_close(&decoder);
		return AVERROR(ENOMEM);
	}
	int64_t source_sample = 0;
	int ret = 0;
	for (;;) {
		cancel_ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
		if (cancel_ret < 0) {
			ret = cancel_ret;
			break;
		}
		int64_t block_start = 0;
		int frames = lj_audio_read(decoder, samples, 8192, &block_start,
			state->error, sizeof(state->error));
		if (frames < 0) {
			ret = AVERROR_EXTERNAL;
			break;
		}
		if (frames == 0)
			break;
		source_sample = block_start;
		int64_t source_samples = timeline->sample_end[timeline->count - 1];
		if (source_samples > 0)
			lj_report_progress(progress, (int)av_rescale(source_sample, 50, source_samples),
				last_progress);
		int offset = 0;
		while (offset < frames) {
			cancel_ret = lj_abort_if_cancelled(state->cancel, state->error,
				sizeof(state->error));
			if (cancel_ret < 0) {
				ret = cancel_ret;
				break;
			}
			size_t index = 0;
			if (!lj_timeline_lookup_sample(timeline, source_sample, &index)) {
				offset = frames;
				break;
			}
			int64_t boundary = timeline->sample_end[index];
			int64_t available = boundary - source_sample;
			if (available <= 0) {
				if (timeline->cursor + 1 < timeline->count)
					++timeline->cursor;
				else {
					offset = frames;
					break;
				}
				continue;
			}
			int take = (int)available;
			if (take > frames - offset)
				take = frames - offset;
			if (timeline->segments[index].action == 0) {
				ret = lj_audio_fifo_append(state,
					samples + (size_t)offset * channels, (size_t)take);
				if (ret >= 0)
					ret = lj_encode_audio_available(state, 0);
			}
			if (ret < 0) {
				offset = frames;
				break;
			}
			offset += take;
			source_sample += take;
		}
		if (ret < 0)
			break;
	}
	if (ret >= 0)
		ret = lj_encode_audio_available(state, 1);
	if (ret >= 0)
		ret = lj_drain_audio_encoder(state);
	free(samples);
	lj_audio_close(&decoder);
	return ret;
}

static int lj_render_video(LJRenderState *state, const char *input,
	LJTimelineView *timeline, LJProgressCallback progress,
	int *last_progress)
{
	AVFormatContext *format = NULL;
	AVCodecContext *decoder = NULL;
	AVPacket *packet = NULL;
	AVFrame *frame = NULL;
	int ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
	if (ret < 0)
		goto fail;
	format = avformat_alloc_context();
	if (!format) {
		ret = AVERROR(ENOMEM);
		goto fail;
	}
	lj_set_interrupt_callback(format, state->cancel);
	ret = avformat_open_input(&format, input, NULL, NULL);
	if (ret < 0)
		goto fail;
	ret = avformat_find_stream_info(format, NULL);
	if (ret < 0)
		goto fail;
	const AVCodec *decoder_codec = NULL;
	int stream_index = av_find_best_stream(format, AVMEDIA_TYPE_VIDEO, -1, -1,
		&decoder_codec, 0);
	if (stream_index < 0) {
		ret = AVERROR_STREAM_NOT_FOUND;
		goto fail;
	}
	decoder = avcodec_alloc_context3(decoder_codec);
	if (!decoder) {
		ret = AVERROR(ENOMEM);
		goto fail;
	}
	ret = avcodec_parameters_to_context(decoder,
		format->streams[stream_index]->codecpar);
	if (ret < 0)
		goto fail;
	decoder->pkt_timebase = format->streams[stream_index]->time_base;
	ret = avcodec_open2(decoder, decoder_codec, NULL);
	if (ret < 0)
		goto fail;
	packet = av_packet_alloc();
	frame = av_frame_alloc();
	if (!packet || !frame) {
		ret = AVERROR(ENOMEM);
		goto fail;
	}
	AVRational fps = av_guess_frame_rate(format, format->streams[stream_index], NULL);
	int64_t fallback_ns = 0;
	int64_t frame_duration_ns = fps.num > 0 && fps.den > 0
		? av_rescale_q(1, (AVRational){fps.den, fps.num},
				(AVRational){1, 1000000000})
		: 33333333;
	for (;;) {
		ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
		if (ret < 0)
			goto fail;
		ret = av_read_frame(format, packet);
		if (ret == AVERROR_EOF)
			break;
		if (ret < 0)
			goto fail;
		if (packet->stream_index != stream_index) {
			av_packet_unref(packet);
			continue;
		}
		ret = avcodec_send_packet(decoder, packet);
		av_packet_unref(packet);
		if (ret < 0)
			goto fail;
		for (;;) {
			ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
			if (ret < 0)
				goto fail;
			ret = avcodec_receive_frame(decoder, frame);
			if (ret == AVERROR(EAGAIN))
				break;
			if (ret < 0)
				goto fail;
			int64_t source_pts = frame->best_effort_timestamp;
			int64_t source_ns;
			if (source_pts == AV_NOPTS_VALUE) {
				source_ns = fallback_ns;
			} else {
				source_ns = av_rescale_q(source_pts, format->streams[stream_index]->time_base,
					(AVRational){1, 1000000000});
			}
			fallback_ns = source_ns + frame_duration_ns;
			lj_report_progress(progress,
				50 + (int)av_rescale(source_ns, 50, timeline->source_end),
				last_progress);
			size_t index = 0;
			if (lj_timeline_lookup_ns(timeline, source_ns, &index) &&
				timeline->segments[index].action == 0) {
				int64_t output_ns = lj_timeline_map_ns(timeline, index, source_ns);
				int64_t output_pts = av_rescale_q(output_ns, (AVRational){1, 1000000000},
					state->video_encoder->time_base);
				if (state->last_video_pts != AV_NOPTS_VALUE && output_pts <= state->last_video_pts)
					output_pts = state->last_video_pts + 1;
				ret = lj_encode_video_frame(state, frame, output_pts);
				if (ret < 0)
					goto fail;
				state->last_video_pts = output_pts;
			}
			av_frame_unref(frame);
		}
	}
	ret = avcodec_send_packet(decoder, NULL);
	if (ret < 0 && ret != AVERROR_EOF)
		goto fail;
	for (;;) {
		ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
		if (ret < 0)
			goto fail;
		ret = avcodec_receive_frame(decoder, frame);
		if (ret == AVERROR_EOF || ret == AVERROR(EAGAIN)) {
			ret = 0;
			break;
		}
		if (ret < 0)
			goto fail;
		int64_t source_pts = frame->best_effort_timestamp;
		int64_t source_ns = source_pts == AV_NOPTS_VALUE
			? fallback_ns
			: av_rescale_q(source_pts, format->streams[stream_index]->time_base,
					(AVRational){1, 1000000000});
		fallback_ns = source_ns + frame_duration_ns;
		lj_report_progress(progress,
			50 + (int)av_rescale(source_ns, 50, timeline->source_end),
			last_progress);
		size_t index = 0;
		if (lj_timeline_lookup_ns(timeline, source_ns, &index) &&
			timeline->segments[index].action == 0) {
			int64_t output_ns = lj_timeline_map_ns(timeline, index, source_ns);
			int64_t output_pts = av_rescale_q(output_ns, (AVRational){1, 1000000000},
				state->video_encoder->time_base);
			if (state->last_video_pts != AV_NOPTS_VALUE && output_pts <= state->last_video_pts)
				output_pts = state->last_video_pts + 1;
			ret = lj_encode_video_frame(state, frame, output_pts);
			if (ret < 0)
				goto fail;
			state->last_video_pts = output_pts;
		}
		av_frame_unref(frame);
	}
	ret = avcodec_send_frame(state->video_encoder, NULL);
	if (ret < 0 && ret != AVERROR_EOF)
		goto fail;
	ret = lj_drain_video(state);

fail:
	if (ret < 0 && !state->error[0]) {
		if (lj_cancel_requested(state->cancel))
			lj_error(state->error, sizeof(state->error), "operation cancelled");
		else
			lj_fferror(state->error, sizeof(state->error), "rendering video", ret);
	}
	av_frame_free(&frame);
	av_packet_free(&packet);
	avcodec_free_context(&decoder);
	avformat_close_input(&format);
	return ret;
}

int lj_render_file(const char *input, const char *output,
	const LJSegment *segments, size_t segment_count,
	const LJRenderOptions *options, LJProgressCallback progress,
	char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	if (!input || !output || !segments || segment_count == 0 || !options ||
		!options->video_codec) {
		lj_error(error, error_size, "rendering: invalid argument");
		return AVERROR(EINVAL);
	}
	LJRenderState state;
	memset(&state, 0, sizeof(state));
	state.cancel = options->cancel;
	state.last_video_pts = AV_NOPTS_VALUE;
	int last_progress = -1;
	LJTimelineView timeline;
	memset(&timeline, 0, sizeof(timeline));
	int ret = 0;
	ret = lj_abort_if_cancelled(state.cancel, state.error, sizeof(state.error));
	if (ret < 0)
		goto done;
	lj_report_progress(progress, 0, &last_progress);
	ret = lj_timeline_init(&timeline, segments, segment_count);
	if (ret < 0) {
		lj_fferror(error, error_size, "validating timeline", ret);
		return ret;
	}
	ret = avformat_alloc_output_context2(&state.output, NULL, NULL, output);
	if (ret < 0 || !state.output) {
		if (ret >= 0)
			ret = AVERROR_UNKNOWN;
		lj_fferror(error, error_size, "creating output context", ret);
		goto done;
	}
	lj_set_interrupt_callback(state.output, state.cancel);
	// Open input to inspect streams and configure encoders before writing header.
	AVFormatContext *input_format = avformat_alloc_context();
	if (!input_format) {
		ret = AVERROR(ENOMEM);
		goto done;
	}
	lj_set_interrupt_callback(input_format, state.cancel);
	ret = avformat_open_input(&input_format, input, NULL, NULL);
	if (ret < 0)
		goto done;
	ret = avformat_find_stream_info(input_format, NULL);
	if (ret < 0) {
		avformat_close_input(&input_format);
		goto done;
	}
	int video_index = av_find_best_stream(input_format, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
	if (video_index < 0) {
		avformat_close_input(&input_format);
		lj_error(state.error, sizeof(state.error), "rendering: input has no video stream");
		ret = AVERROR_STREAM_NOT_FOUND;
		goto done;
	}
	int has_audio = av_find_best_stream(input_format, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0) >= 0;
	int vid_w = input_format->streams[video_index]->codecpar->width;
	int vid_h = input_format->streams[video_index]->codecpar->height;
	AVRational vid_fps = av_guess_frame_rate(input_format, input_format->streams[video_index], NULL);
	avformat_close_input(&input_format);

	ret = lj_open_video_encoder(&state, options, vid_w, vid_h, vid_fps);
	if (ret < 0)
		goto done;
	if (has_audio) {
		ret = lj_open_audio_encoder(&state, options->output_mono ? 1 : 2);
		if (ret < 0)
			goto done;
	}
	if (!(state.output->oformat->flags & AVFMT_NOFILE)) {
		ret = avio_open2(&state.output->pb, output, AVIO_FLAG_WRITE,
			&state.output->interrupt_callback, NULL);
		if (ret < 0)
			goto done;
	}
	ret = lj_abort_if_cancelled(state.cancel, state.error, sizeof(state.error));
	if (ret < 0)
		goto done;
	ret = avformat_write_header(state.output, NULL);
	if (ret < 0)
		goto done;
	if (has_audio) {
		ret = lj_render_audio(&state, input, options, &timeline, progress, &last_progress);
		if (ret < 0)
			goto done;
	} else {
		lj_report_progress(progress, 50, &last_progress);
	}
	ret = lj_abort_if_cancelled(state.cancel, state.error, sizeof(state.error));
	if (ret < 0)
		goto done;
	timeline.cursor = 0;
	ret = lj_render_video(&state, input, &timeline, progress, &last_progress);
	if (ret < 0)
		goto done;
	ret = lj_abort_if_cancelled(state.cancel, state.error, sizeof(state.error));
	if (ret >= 0)
		ret = av_write_trailer(state.output);
	if (ret >= 0)
		lj_report_progress(progress, 100, &last_progress);

done:
	if (ret < 0) {
		if (state.error[0])
			lj_error(error, error_size, "%s", state.error);
		else if (lj_cancel_requested(state.cancel))
			lj_error(error, error_size, "operation cancelled");
		else
			lj_fferror(error, error_size, "rendering", ret);
	}
	if (state.output) {
		if (!(state.output->oformat->flags & AVFMT_NOFILE)) {
			int close_ret = avio_closep(&state.output->pb);
			if (ret >= 0 && close_ret < 0) {
				ret = close_ret;
				if (lj_cancel_requested(state.cancel))
					lj_error(error, error_size, "operation cancelled");
				else
					lj_fferror(error, error_size, "closing output", ret);
			}
		}
		avformat_free_context(state.output);
	}
	sws_freeContext(state.scaler);
	swr_free(&state.audio_resampler);
	avcodec_free_context(&state.video_encoder);
	avcodec_free_context(&state.audio_encoder);
	av_buffer_unref(&state.hw_frames);
	av_buffer_unref(&state.vaapi_device);
	free(state.audio_fifo);
	lj_timeline_free(&timeline);
	return ret;
}

int lj_remux_hevc_hvc1(const char *input, const char *output,
	LJCancelToken *cancel, char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	if (!input || !output) {
		lj_error(error, error_size, "remuxing: invalid argument");
		return AVERROR(EINVAL);
	}

	AVFormatContext *input_format = NULL;
	AVFormatContext *output_format = NULL;
	AVPacket *packet = NULL;
	int ret = lj_abort_if_cancelled(cancel, error, error_size);
	int has_hevc = 0;
	if (ret < 0)
		goto done;

	input_format = avformat_alloc_context();
	if (!input_format) {
		ret = AVERROR(ENOMEM);
		goto done;
	}
	lj_set_interrupt_callback(input_format, cancel);
	ret = avformat_open_input(&input_format, input, NULL, NULL);
	if (ret < 0)
		goto done;
	ret = avformat_find_stream_info(input_format, NULL);
	if (ret < 0)
		goto done;

	ret = avformat_alloc_output_context2(&output_format, NULL, "mp4", output);
	if (ret < 0 || !output_format) {
		if (ret >= 0)
			ret = AVERROR_UNKNOWN;
		goto done;
	}
	lj_set_interrupt_callback(output_format, cancel);
	ret = av_dict_copy(&output_format->metadata, input_format->metadata, 0);
	if (ret < 0)
		goto done;

	for (unsigned int i = 0; i < input_format->nb_streams; ++i) {
		const AVStream *input_stream = input_format->streams[i];
		AVStream *output_stream = avformat_new_stream(output_format, NULL);
		if (!output_stream) {
			ret = AVERROR(ENOMEM);
			goto done;
		}
		ret = avcodec_parameters_copy(output_stream->codecpar, input_stream->codecpar);
		if (ret < 0)
			goto done;
		output_stream->id = input_stream->id;
		output_stream->time_base = input_stream->time_base;
		output_stream->start_time = input_stream->start_time;
		output_stream->duration = input_stream->duration;
		output_stream->nb_frames = input_stream->nb_frames;
		output_stream->disposition = input_stream->disposition;
		output_stream->discard = input_stream->discard;
		output_stream->sample_aspect_ratio = input_stream->sample_aspect_ratio;
		output_stream->avg_frame_rate = input_stream->avg_frame_rate;
		output_stream->r_frame_rate = input_stream->r_frame_rate;
		ret = av_dict_copy(&output_stream->metadata, input_stream->metadata, 0);
		if (ret < 0)
			goto done;
		if (input_stream->disposition & AV_DISPOSITION_ATTACHED_PIC) {
			ret = av_packet_ref(&output_stream->attached_pic, &input_stream->attached_pic);
			if (ret < 0)
				goto done;
		}
		if (input_stream->codecpar->codec_type == AVMEDIA_TYPE_VIDEO &&
			input_stream->codecpar->codec_id == AV_CODEC_ID_HEVC) {
			output_stream->codecpar->codec_tag = lj_fourcc("hvc1");
			has_hevc = 1;
		}
	}
	if (!has_hevc) {
		lj_error(error, error_size, "remuxing: input contains no HEVC video stream");
		ret = AVERROR_STREAM_NOT_FOUND;
		goto done;
	}

	if (!(output_format->oformat->flags & AVFMT_NOFILE)) {
		ret = avio_open2(&output_format->pb, output, AVIO_FLAG_WRITE,
			&output_format->interrupt_callback, NULL);
		if (ret < 0)
			goto done;
	}
	ret = lj_abort_if_cancelled(cancel, error, error_size);
	if (ret < 0)
		goto done;
	ret = avformat_write_header(output_format, NULL);
	if (ret < 0)
		goto done;

	packet = av_packet_alloc();
	if (!packet) {
		ret = AVERROR(ENOMEM);
		goto done;
	}
	for (;;) {
		ret = lj_abort_if_cancelled(cancel, error, error_size);
		if (ret < 0)
			goto done;
		ret = av_read_frame(input_format, packet);
		if (ret == AVERROR_EOF) {
			ret = 0;
			break;
		}
		if (ret < 0)
			goto done;
		ret = lj_abort_if_cancelled(cancel, error, error_size);
		if (ret < 0) {
			av_packet_unref(packet);
			goto done;
		}
		if (packet->stream_index < 0 ||
			(unsigned int)packet->stream_index >= input_format->nb_streams) {
			lj_error(error, error_size, "remuxing: packet has invalid stream index %d",
				packet->stream_index);
			ret = AVERROR_INVALIDDATA;
			av_packet_unref(packet);
			goto done;
		}
		AVStream *input_stream = input_format->streams[packet->stream_index];
		AVStream *output_stream = output_format->streams[packet->stream_index];
		av_packet_rescale_ts(packet, input_stream->time_base, output_stream->time_base);
		packet->stream_index = output_stream->index;
		packet->pos = -1;
		ret = av_interleaved_write_frame(output_format, packet);
		av_packet_unref(packet);
		if (ret < 0)
			goto done;
	}
	ret = lj_abort_if_cancelled(cancel, error, error_size);
	if (ret >= 0)
		ret = av_write_trailer(output_format);
	if (ret >= 0)
		ret = lj_abort_if_cancelled(cancel, error, error_size);

done:
	av_packet_free(&packet);
	avformat_close_input(&input_format);
	if (output_format) {
		if (!(output_format->oformat->flags & AVFMT_NOFILE)) {
			int close_ret = avio_closep(&output_format->pb);
			if (ret >= 0 && close_ret < 0) {
				ret = close_ret;
				if (lj_cancel_requested(cancel))
					lj_error(error, error_size, "operation cancelled");
				else
					lj_fferror(error, error_size, "closing remux output", ret);
			}
		}
		avformat_free_context(output_format);
	}
	if (ret < 0 && (!error || error_size == 0 || !error[0])) {
		if (lj_cancel_requested(cancel))
			lj_error(error, error_size, "operation cancelled");
		else if (ret == AVERROR_EOF)
			lj_error(error, error_size, "remuxing: unexpected end of input");
		else
			lj_fferror(error, error_size, "remuxing", ret);
	}
	return ret;
}
