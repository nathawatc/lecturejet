#include "internal.h"

typedef struct LJRenderState {
	AVFormatContext *output;
	LJCancelToken *cancel;
	AVStream *video_stream;
	AVStream *audio_stream;
	AVCodecContext *video_encoder;
	AVCodecContext *audio_encoder;
	AVBufferRef *vaapi_device;
	AVBufferRef *hw_frames;
	AVFilterGraph *filter_graph;
	AVFilterContext *buffersrc_ctx;
	AVFilterContext *buffersink_ctx;
	int filter_in_format;
	int filter_in_width;
	int filter_in_height;
	enum AVPixelFormat filter_in_sw_format;
	AVFrame *filtered_frame;
	SwrContext *audio_resampler;
	AVPacket *video_packet;
	AVPacket *audio_packet;
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
	if (!state->audio_packet) {
		state->audio_packet = av_packet_alloc();
		if (!state->audio_packet)
			return AVERROR(ENOMEM);
	}
	for (;;) {
		int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error,
			sizeof(state->error));
		if (cancel_ret < 0)
			return cancel_ret;
		int ret = avcodec_receive_packet(state->audio_encoder, state->audio_packet);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
			return 0;
		if (ret < 0)
			return ret;
		if (final_valid_samples > 0 && final_valid_samples < state->audio_frame_size)
			state->audio_packet->duration = final_valid_samples;
		ret = lj_write_packet(state, state->audio_packet, state->audio_stream,
			state->audio_encoder->time_base);
		av_packet_unref(state->audio_packet);
		if (ret < 0)
			return ret;
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
	if (!state->video_packet) {
		state->video_packet = av_packet_alloc();
		if (!state->video_packet)
			return AVERROR(ENOMEM);
	}
	for (;;) {
		int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error,
			sizeof(state->error));
		if (cancel_ret < 0)
			return cancel_ret;
		int ret = avcodec_receive_packet(state->video_encoder, state->video_packet);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
			return 0;
		if (ret < 0)
			return ret;
		ret = lj_write_packet(state, state->video_packet, state->video_stream,
			state->video_encoder->time_base);
		av_packet_unref(state->video_packet);
		if (ret < 0)
			return ret;
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
		state->video_stream->codecpar->codec_tag =
			strcmp(tag, "hvc1") == 0 ? MKTAG('h', 'v', 'c', '1') : MKTAG('h', 'e', 'v', '1');
	}
	state->video_stream->time_base = state->video_encoder->time_base;
	return 0;
}

static int lj_send_and_drain_video(LJRenderState *state, AVFrame *frame)
{
	int ret;
	for (;;) {
		int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error,
			sizeof(state->error));
		if (cancel_ret < 0)
			return cancel_ret;
		ret = avcodec_send_frame(state->video_encoder, frame);
		if (ret == AVERROR(EAGAIN)) {
			int drain_ret = lj_drain_video(state);
			if (drain_ret < 0)
				return drain_ret;
			continue;
		}
		break;
	}
	if (ret < 0)
		return ret;
	return lj_drain_video(state);
}

static int lj_frame_needs_scaling(const LJRenderState *state, const AVFrame *frame)
{
	if (frame->format != AV_PIX_FMT_VAAPI)
		return 1;
	if (frame->width != state->video_encoder->width ||
	    frame->height != state->video_encoder->height)
		return 1;
	if (!frame->hw_frames_ctx)
		return 1;
	AVHWFramesContext *hwfc = (AVHWFramesContext *)frame->hw_frames_ctx->data;
	if (hwfc->sw_format != AV_PIX_FMT_NV12)
		return 1;
	return 0;
}

static int lj_filter_matches(const LJRenderState *state, const AVFrame *frame)
{
	if (!state->filter_graph)
		return 0;
	if (state->filter_in_format != frame->format ||
	    state->filter_in_width != frame->width ||
	    state->filter_in_height != frame->height)
		return 0;
	if (frame->format == AV_PIX_FMT_VAAPI) {
		enum AVPixelFormat sw_fmt = AV_PIX_FMT_NONE;
		if (frame->hw_frames_ctx) {
			AVHWFramesContext *hwfc = (AVHWFramesContext *)frame->hw_frames_ctx->data;
			sw_fmt = hwfc->sw_format;
		}
		if (state->filter_in_sw_format != sw_fmt)
			return 0;
	}
	return 1;
}

static int lj_init_video_filter(LJRenderState *state, const AVFrame *frame)
{
	if (state->filter_graph) {
		avfilter_graph_free(&state->filter_graph);
		state->buffersrc_ctx = NULL;
		state->buffersink_ctx = NULL;
	}

	AVFilterGraph *graph = avfilter_graph_alloc();
	if (!graph)
		return AVERROR(ENOMEM);

	const AVFilter *buffersrc = avfilter_get_by_name("buffer");
	const AVFilter *hwupload = avfilter_get_by_name("hwupload");
	const AVFilter *scale_vaapi = avfilter_get_by_name("scale_vaapi");
	const AVFilter *buffersink = avfilter_get_by_name("buffersink");
	if (!buffersrc || !hwupload || !scale_vaapi || !buffersink) {
		avfilter_graph_free(&graph);
		return AVERROR_FILTER_NOT_FOUND;
	}

	AVFilterContext *src_ctx = avfilter_graph_alloc_filter(graph, buffersrc, "in");
	if (!src_ctx) {
		avfilter_graph_free(&graph);
		return AVERROR(ENOMEM);
	}

	AVBufferSrcParameters *params = av_buffersrc_parameters_alloc();
	if (!params) {
		avfilter_graph_free(&graph);
		return AVERROR(ENOMEM);
	}
	params->format = frame->format;
	params->width = frame->width;
	params->height = frame->height;
	params->time_base = state->video_encoder->time_base;
	params->sample_aspect_ratio = frame->sample_aspect_ratio.num > 0 && frame->sample_aspect_ratio.den > 0
		? frame->sample_aspect_ratio : (AVRational){1, 1};
	params->color_space = frame->colorspace;
	params->color_range = frame->color_range;
	if (frame->format == AV_PIX_FMT_VAAPI && frame->hw_frames_ctx)
		params->hw_frames_ctx = frame->hw_frames_ctx;

	int ret = av_buffersrc_parameters_set(src_ctx, params);
	av_free(params);
	if (ret < 0) {
		avfilter_graph_free(&graph);
		return ret;
	}

	ret = avfilter_init_str(src_ctx, NULL);
	if (ret < 0) {
		avfilter_graph_free(&graph);
		return ret;
	}

	AVFilterContext *upload_ctx = NULL;
	if (frame->format != AV_PIX_FMT_VAAPI) {
		upload_ctx = avfilter_graph_alloc_filter(graph, hwupload, "upload");
		if (!upload_ctx) {
			avfilter_graph_free(&graph);
			return AVERROR(ENOMEM);
		}
		upload_ctx->hw_device_ctx = av_buffer_ref(state->vaapi_device);
		if (!upload_ctx->hw_device_ctx) {
			avfilter_graph_free(&graph);
			return AVERROR(ENOMEM);
		}
		ret = avfilter_init_str(upload_ctx, NULL);
		if (ret < 0) {
			avfilter_graph_free(&graph);
			return ret;
		}
	}

	AVFilterContext *scale_ctx = avfilter_graph_alloc_filter(graph, scale_vaapi, "scale");
	if (!scale_ctx) {
		avfilter_graph_free(&graph);
		return AVERROR(ENOMEM);
	}
	scale_ctx->hw_device_ctx = av_buffer_ref(state->vaapi_device);
	if (!scale_ctx->hw_device_ctx) {
		avfilter_graph_free(&graph);
		return AVERROR(ENOMEM);
	}
	char scale_args[128];
	snprintf(scale_args, sizeof(scale_args), "w=%d:h=%d:format=nv12",
		state->video_encoder->width, state->video_encoder->height);
	ret = avfilter_init_str(scale_ctx, scale_args);
	if (ret < 0) {
		avfilter_graph_free(&graph);
		return ret;
	}

	AVFilterContext *sink_ctx = NULL;
	ret = avfilter_graph_create_filter(&sink_ctx, buffersink, "out",
		"pixel_formats=vaapi", NULL, graph);
	if (ret < 0) {
		avfilter_graph_free(&graph);
		return ret;
	}

	if (upload_ctx) {
		ret = avfilter_link(src_ctx, 0, upload_ctx, 0);
		if (ret >= 0)
			ret = avfilter_link(upload_ctx, 0, scale_ctx, 0);
	} else {
		ret = avfilter_link(src_ctx, 0, scale_ctx, 0);
	}
	if (ret >= 0)
		ret = avfilter_link(scale_ctx, 0, sink_ctx, 0);
	if (ret < 0) {
		avfilter_graph_free(&graph);
		return ret;
	}

	ret = avfilter_graph_config(graph, NULL);
	if (ret < 0) {
		lj_fferror(state->error, sizeof(state->error), "configuring video filter graph", ret);
		avfilter_graph_free(&graph);
		return ret;
	}

	state->filter_graph = graph;
	state->buffersrc_ctx = src_ctx;
	state->buffersink_ctx = sink_ctx;
	state->filter_in_format = frame->format;
	state->filter_in_width = frame->width;
	state->filter_in_height = frame->height;
	state->filter_in_sw_format = AV_PIX_FMT_NONE;
	if (frame->format == AV_PIX_FMT_VAAPI && frame->hw_frames_ctx) {
		AVHWFramesContext *hwfc = (AVHWFramesContext *)frame->hw_frames_ctx->data;
		state->filter_in_sw_format = hwfc->sw_format;
	}

	return 0;
}

static int lj_encode_video_frame(LJRenderState *state, AVFrame *decoded,
	int64_t output_pts)
{
	int ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
	if (ret < 0)
		return ret;

	if (!lj_frame_needs_scaling(state, decoded)) {
		decoded->pts = output_pts;
		ret = lj_send_and_drain_video(state, decoded);
		if (ret >= 0)
			state->last_video_pts = output_pts;
		return ret;
	}

	if (!lj_filter_matches(state, decoded)) {
		ret = lj_init_video_filter(state, decoded);
		if (ret < 0)
			return ret;
	}
	if (!state->filtered_frame) {
		state->filtered_frame = av_frame_alloc();
		if (!state->filtered_frame)
			return AVERROR(ENOMEM);
	}

	decoded->pts = output_pts;
	ret = av_buffersrc_add_frame_flags(state->buffersrc_ctx, decoded,
		AV_BUFFERSRC_FLAG_KEEP_REF);
	if (ret < 0)
		return ret;

	for (;;) {
		ret = av_buffersink_get_frame(state->buffersink_ctx, state->filtered_frame);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
			return 0;
		if (ret < 0)
			return ret;
		state->filtered_frame->pts = output_pts;
		ret = lj_send_and_drain_video(state, state->filtered_frame);
		av_frame_unref(state->filtered_frame);
		if (ret < 0)
			return ret;
		state->last_video_pts = output_pts;
	}
}

static int lj_process_audio_samples(LJRenderState *state, LJAudioDecoder *audio_decoder,
	LJTimelineView *timeline, float *audio_samples, int audio_channels,
	int64_t *audio_pre_roll_sample)
{
	for (;;) {
		int cancel_ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
		if (cancel_ret < 0)
			return cancel_ret;
		int64_t block_start = 0;
		int frames = lj_audio_read(audio_decoder, audio_samples, 8192,
			&block_start, state->error, sizeof(state->error));
		if (frames < 0)
			return AVERROR_EXTERNAL;
		if (frames == 0)
			break;
		int64_t source_sample = block_start;
		int offset = 0;
		while (offset < frames) {
			cancel_ret = lj_abort_if_cancelled(state->cancel, state->error, sizeof(state->error));
			if (cancel_ret < 0)
				return cancel_ret;
			if (*audio_pre_roll_sample >= 0) {
				if (source_sample < *audio_pre_roll_sample) {
					int skip = (int)(*audio_pre_roll_sample - source_sample);
					if (skip > frames - offset)
						skip = frames - offset;
					offset += skip;
					source_sample += skip;
					continue;
				}
				*audio_pre_roll_sample = -1;
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
				int ret = lj_audio_fifo_append(state,
					audio_samples + (size_t)offset * audio_channels, (size_t)take);
				if (ret >= 0)
					ret = lj_encode_audio_available(state, 0);
				if (ret < 0)
					return ret;
			}
			offset += take;
			source_sample += take;
		}
	}
	return 0;
}

static enum AVPixelFormat lj_get_hw_format(AVCodecContext *ctx,
	const enum AVPixelFormat *pix_fmts)
{
	(void)ctx;
	const enum AVPixelFormat *p;
	for (p = pix_fmts; *p != AV_PIX_FMT_NONE; p++) {
		if (*p == AV_PIX_FMT_VAAPI)
			return AV_PIX_FMT_VAAPI;
	}
	return pix_fmts[0];
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

	AVFormatContext *input_format = NULL;
	AVCodecContext *video_decoder = NULL;
	LJAudioDecoder *audio_decoder = NULL;
	AVPacket *demux_packet = NULL;
	AVFrame *video_frame = NULL;
	float *audio_samples = NULL;

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

	input_format = avformat_alloc_context();
	if (!input_format) {
		ret = AVERROR(ENOMEM);
		goto done;
	}
	lj_set_interrupt_callback(input_format, state.cancel);
	ret = avformat_open_input(&input_format, input, NULL, NULL);
	if (ret < 0)
		goto done;
	ret = avformat_find_stream_info(input_format, NULL);
	if (ret < 0)
		goto done;

	int video_index = av_find_best_stream(input_format, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
	if (video_index < 0) {
		lj_error(state.error, sizeof(state.error), "rendering: input has no video stream");
		ret = AVERROR_STREAM_NOT_FOUND;
		goto done;
	}
	int audio_index = av_find_best_stream(input_format, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);
	int has_audio = (audio_index >= 0);

	int vid_w = input_format->streams[video_index]->codecpar->width;
	int vid_h = input_format->streams[video_index]->codecpar->height;
	AVRational vid_fps = av_guess_frame_rate(input_format, input_format->streams[video_index], NULL);

	ret = lj_open_video_encoder(&state, options, vid_w, vid_h, vid_fps);
	if (ret < 0)
		goto done;

	int audio_channels = options->output_mono ? 1 : 2;
	if (has_audio) {
		ret = lj_open_audio_encoder(&state, audio_channels);
		if (ret < 0)
			goto done;
	}

	const AVCodec *decoder_codec = avcodec_find_decoder(
		input_format->streams[video_index]->codecpar->codec_id);
	if (!decoder_codec) {
		ret = AVERROR_DECODER_NOT_FOUND;
		goto done;
	}
	video_decoder = avcodec_alloc_context3(decoder_codec);
	if (!video_decoder) {
		ret = AVERROR(ENOMEM);
		goto done;
	}
	ret = avcodec_parameters_to_context(video_decoder,
		input_format->streams[video_index]->codecpar);
	if (ret < 0) {
		avcodec_free_context(&video_decoder);
		goto done;
	}
	video_decoder->pkt_timebase = input_format->streams[video_index]->time_base;
	video_decoder->thread_count = options->threads > 0 ? options->threads : 0;
	video_decoder->thread_type = FF_THREAD_FRAME | FF_THREAD_SLICE;
	if (state.vaapi_device) {
		video_decoder->hw_device_ctx = av_buffer_ref(state.vaapi_device);
		video_decoder->get_format = lj_get_hw_format;
	}
	ret = avcodec_open2(video_decoder, decoder_codec, NULL);
	if (ret < 0 && state.vaapi_device) {
		avcodec_free_context(&video_decoder);
		video_decoder = avcodec_alloc_context3(decoder_codec);
		if (!video_decoder) {
			ret = AVERROR(ENOMEM);
			goto done;
		}
		ret = avcodec_parameters_to_context(video_decoder,
			input_format->streams[video_index]->codecpar);
		if (ret < 0) {
			avcodec_free_context(&video_decoder);
			goto done;
		}
		video_decoder->pkt_timebase = input_format->streams[video_index]->time_base;
		video_decoder->thread_count = options->threads > 0 ? options->threads : 0;
		video_decoder->thread_type = FF_THREAD_FRAME | FF_THREAD_SLICE;
		ret = avcodec_open2(video_decoder, decoder_codec, NULL);
	}
	if (ret < 0)
		goto done;

	if (has_audio) {
		audio_decoder = lj_audio_open_stream(input_format->streams[audio_index],
			audio_channels, state.cancel, options->rnnoise_enabled,
			options->rnnoise_strength, options->rnnoise_model,
			state.error, sizeof(state.error));
		if (!audio_decoder) {
			ret = AVERROR_EXTERNAL;
			goto done;
		}
		audio_samples = malloc((size_t)8192 * audio_channels * sizeof(float));
		if (!audio_samples) {
			ret = AVERROR(ENOMEM);
			goto done;
		}
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

	demux_packet = av_packet_alloc();
	video_frame = av_frame_alloc();
	if (!demux_packet || !video_frame) {
		ret = AVERROR(ENOMEM);
		goto done;
	}

	int64_t fallback_ns = 0;
	int64_t frame_duration_ns = vid_fps.num > 0 && vid_fps.den > 0
		? av_rescale_q(1, (AVRational){vid_fps.den, vid_fps.num},
				(AVRational){1, 1000000000})
		: 33333333;
	int64_t pre_roll_target_ns = -1;
	int64_t audio_pre_roll_sample = -1;
	int eof_reached = 0;

	for (;;) {
		ret = lj_abort_if_cancelled(state.cancel, state.error, sizeof(state.error));
		if (ret < 0)
			goto done;
		ret = av_read_frame(input_format, demux_packet);
		if (ret == AVERROR_EOF) {
			ret = 0;
			break;
		}
		if (ret < 0)
			goto done;

		if (demux_packet->stream_index == video_index) {
			ret = avcodec_send_packet(video_decoder, demux_packet);
			av_packet_unref(demux_packet);
			if (ret < 0)
				goto done;
			for (;;) {
				ret = lj_abort_if_cancelled(state.cancel, state.error, sizeof(state.error));
				if (ret < 0)
					goto done;
				ret = avcodec_receive_frame(video_decoder, video_frame);
				if (ret == AVERROR(EAGAIN)) {
					ret = 0;
					break;
				}
				if (ret < 0)
					goto done;
				int64_t source_pts = video_frame->best_effort_timestamp;
				int64_t source_ns;
				if (source_pts == AV_NOPTS_VALUE) {
					source_ns = fallback_ns;
				} else {
					source_ns = av_rescale_q(source_pts, input_format->streams[video_index]->time_base,
						(AVRational){1, 1000000000});
				}
				fallback_ns = source_ns + frame_duration_ns;
				lj_report_progress(progress,
					(int)av_rescale(source_ns, 100, timeline.source_end),
					&last_progress);
				if (pre_roll_target_ns >= 0) {
					if (source_ns < pre_roll_target_ns) {
						av_frame_unref(video_frame);
						continue;
					}
					pre_roll_target_ns = -1;
				}
				size_t index = 0;
				if (lj_timeline_lookup_ns(&timeline, source_ns, &index)) {
					if (timeline.segments[index].action == 0) {
						int64_t output_ns = lj_timeline_map_ns(&timeline, index, source_ns);
						int64_t output_pts = av_rescale_q(output_ns, (AVRational){1, 1000000000},
							state.video_encoder->time_base);
						if (state.last_video_pts != AV_NOPTS_VALUE && output_pts <= state.last_video_pts)
							output_pts = state.last_video_pts + 1;
						ret = lj_encode_video_frame(&state, video_frame, output_pts);
						if (ret < 0) {
							av_frame_unref(video_frame);
							goto done;
						}
						state.last_video_pts = output_pts;
					} else {
						size_t next_keep = 0;
						if (!lj_timeline_next_keep(&timeline, index, &next_keep)) {
							av_log(input_format, AV_LOG_INFO,
								"Trailing silence to EOF (%.2fs); ending video demux\n",
								(double)(timeline.source_end - source_ns) / 1000000000.0);
							av_frame_unref(video_frame);
							eof_reached = 1;
							break;
						}
						int64_t target_ns = timeline.segments[next_keep].start_ns;
						int64_t gap_ns = target_ns - source_ns;
						if (gap_ns >= LJ_MIN_SEEK_SILENCE_NS) {
							int64_t target_pts = av_rescale_q(target_ns, (AVRational){1, 1000000000},
								input_format->streams[video_index]->time_base);
							av_log(input_format, AV_LOG_INFO,
								"Seeking over %.2fs silence to target PTS %" PRId64 " (%.2fs)\n",
								(double)gap_ns / 1000000000.0, target_pts, (double)target_ns / 1000000000.0);
							int seek_ret = av_seek_frame(input_format, video_index, target_pts,
								AVSEEK_FLAG_BACKWARD);
							if (seek_ret < 0) {
								av_log(input_format, AV_LOG_WARNING,
									"Seek to target PTS %" PRId64 " failed (%d); continuing linear decode\n",
									target_pts, seek_ret);
							} else {
								avcodec_flush_buffers(video_decoder);
								pre_roll_target_ns = target_ns;
								fallback_ns = target_ns;
								timeline.cursor = next_keep;
								if (has_audio) {
									int64_t target_sample = timeline.sample_start[next_keep];
									lj_audio_seek_reset(audio_decoder, -1);
									audio_pre_roll_sample = target_sample;
								}
								av_frame_unref(video_frame);
								break;
							}
						}
					}
				}
				av_frame_unref(video_frame);
			}
			if (ret < 0)
				goto done;
		} else if (has_audio && demux_packet->stream_index == audio_index) {
			ret = lj_audio_send_packet(audio_decoder, demux_packet);
			av_packet_unref(demux_packet);
			if (ret < 0)
				goto done;
			ret = lj_process_audio_samples(&state, audio_decoder, &timeline,
				audio_samples, audio_channels, &audio_pre_roll_sample);
			if (ret < 0)
				goto done;
		} else {
			av_packet_unref(demux_packet);
		}

		if (eof_reached)
			break;
	}

	// Drain video decoder
	ret = avcodec_send_packet(video_decoder, NULL);
	if (ret < 0 && ret != AVERROR_EOF)
		goto done;
	for (;;) {
		ret = lj_abort_if_cancelled(state.cancel, state.error, sizeof(state.error));
		if (ret < 0)
			goto done;
		ret = avcodec_receive_frame(video_decoder, video_frame);
		if (ret == AVERROR_EOF || ret == AVERROR(EAGAIN)) {
			ret = 0;
			break;
		}
		if (ret < 0)
			goto done;
		int64_t source_pts = video_frame->best_effort_timestamp;
		int64_t source_ns = source_pts == AV_NOPTS_VALUE
			? fallback_ns
			: av_rescale_q(source_pts, input_format->streams[video_index]->time_base,
					(AVRational){1, 1000000000});
		fallback_ns = source_ns + frame_duration_ns;
		lj_report_progress(progress,
			(int)av_rescale(source_ns, 100, timeline.source_end),
			&last_progress);
		if (pre_roll_target_ns >= 0) {
			if (source_ns < pre_roll_target_ns) {
				av_frame_unref(video_frame);
				continue;
			}
			pre_roll_target_ns = -1;
		}
		size_t index = 0;
		if (lj_timeline_lookup_ns(&timeline, source_ns, &index) &&
			timeline.segments[index].action == 0) {
			int64_t output_ns = lj_timeline_map_ns(&timeline, index, source_ns);
			int64_t output_pts = av_rescale_q(output_ns, (AVRational){1, 1000000000},
				state.video_encoder->time_base);
			if (state.last_video_pts != AV_NOPTS_VALUE && output_pts <= state.last_video_pts)
				output_pts = state.last_video_pts + 1;
			ret = lj_encode_video_frame(&state, video_frame, output_pts);
			if (ret < 0) {
				av_frame_unref(video_frame);
				goto done;
			}
			state.last_video_pts = output_pts;
		}
		av_frame_unref(video_frame);
	}

	// Drain audio decoder and encoders
	if (has_audio) {
		ret = lj_audio_flush(audio_decoder);
		if (ret < 0)
			goto done;
		ret = lj_process_audio_samples(&state, audio_decoder, &timeline,
			audio_samples, audio_channels, &audio_pre_roll_sample);
		if (ret < 0)
			goto done;
		ret = lj_encode_audio_available(&state, 1);
		if (ret < 0)
			goto done;
		ret = lj_drain_audio_encoder(&state);
		if (ret < 0)
			goto done;
	}

	// Flush video filter graph if active
	if (state.buffersrc_ctx) {
		ret = av_buffersrc_add_frame_flags(state.buffersrc_ctx, NULL, 0);
		if (ret < 0 && ret != AVERROR_EOF)
			goto done;
		if (!state.filtered_frame) {
			state.filtered_frame = av_frame_alloc();
			if (!state.filtered_frame) {
				ret = AVERROR(ENOMEM);
				goto done;
			}
		}
		for (;;) {
			ret = av_buffersink_get_frame(state.buffersink_ctx, state.filtered_frame);
			if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
				ret = 0;
				break;
			}
			if (ret < 0)
				goto done;
			if (state.last_video_pts != AV_NOPTS_VALUE &&
			    state.filtered_frame->pts <= state.last_video_pts)
				state.filtered_frame->pts = state.last_video_pts + 1;
			ret = lj_send_and_drain_video(&state, state.filtered_frame);
			if (ret >= 0)
				state.last_video_pts = state.filtered_frame->pts;
			av_frame_unref(state.filtered_frame);
			if (ret < 0)
				goto done;
		}
	}

	// Flush video encoder
	ret = avcodec_send_frame(state.video_encoder, NULL);
	if (ret < 0 && ret != AVERROR_EOF)
		goto done;
	ret = lj_drain_video(&state);
	if (ret < 0)
		goto done;

	// Trailer
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
	av_frame_free(&video_frame);
	av_packet_free(&demux_packet);
	free(audio_samples);
	lj_audio_close(&audio_decoder);
	avcodec_free_context(&video_decoder);
	if (input_format)
		avformat_close_input(&input_format);
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
	av_frame_free(&state.filtered_frame);
	avfilter_graph_free(&state.filter_graph);
	av_packet_free(&state.video_packet);
	av_packet_free(&state.audio_packet);
	swr_free(&state.audio_resampler);
	avcodec_free_context(&state.video_encoder);
	avcodec_free_context(&state.audio_encoder);
	av_buffer_unref(&state.hw_frames);
	av_buffer_unref(&state.vaapi_device);
	free(state.audio_fifo);
	lj_timeline_free(&timeline);
	return ret;
}
