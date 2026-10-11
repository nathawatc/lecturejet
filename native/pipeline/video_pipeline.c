/* Video path: decode, silence-skipping seek, timeline mapping, filter, encode. */
#include "pipeline.h"

#include "../hw/vaapi_device.h"

#include <inttypes.h>
#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libavutil/mathematics.h>

#define LJ_FALLBACK_FRAME_NS 33333333

int lj_video_open_encoder(LJRenderEngine *engine, const LJRenderOptions *options)
{
	LJVideoPipeline *video = &engine->video;
	AVStream *source = engine->input->streams[video->stream_index];
	int ret = lj_video_profile_select(options->video_codec, options->video_tag, &video->config,
		engine->error, sizeof(engine->error));
	if (ret < 0)
		return ret;
	AVRational fps = av_guess_frame_rate(engine->input, source, NULL);
	video->frame_duration_ns = fps.num > 0 && fps.den > 0
		? av_rescale_q(1, (AVRational){fps.den, fps.num}, LJ_NS_Q) : LJ_FALLBACK_FRAME_NS;
	video->last_dts = AV_NOPTS_VALUE;
	int global_header = (engine->output->oformat->flags & AVFMT_GLOBALHEADER) &&
		video->config.codec_id != AV_CODEC_ID_HEVC;
	ret = lj_video_encoder_open(&video->config, engine->vaapi_device,
		source->codecpar->width, source->codecpar->height, fps,
		global_header, &video->encoder,
		engine->error, sizeof(engine->error));
	if (ret < 0)
		return ret;
	video->out_stream = avformat_new_stream(engine->output, NULL);
	if (!video->out_stream)
		return AVERROR(ENOMEM);
	ret = avcodec_parameters_from_context(video->out_stream->codecpar, video->encoder);
	if (ret < 0)
		return ret;
	if (video->config.codec_id == AV_CODEC_ID_HEVC) {
		av_freep(&video->out_stream->codecpar->extradata);
		video->out_stream->codecpar->extradata_size = 0;
	}
	if (video->config.codec_tag)
		video->out_stream->codecpar->codec_tag = video->config.codec_tag;
	video->out_stream->time_base = video->encoder->time_base;
	return 0;
}

static int lj_video_decoder_create(const AVCodec *codec, const AVStream *stream, int threads,
	AVBufferRef *device_ref, AVCodecContext **decoder)
{
	AVCodecContext *context = avcodec_alloc_context3(codec);
	if (!context)
		return AVERROR(ENOMEM);
	int ret = avcodec_parameters_to_context(context, stream->codecpar);
	if (ret >= 0) {
		context->pkt_timebase = stream->time_base;
		context->thread_count = threads > 0 ? threads : 0;
		context->thread_type = FF_THREAD_FRAME | FF_THREAD_SLICE;
		if (device_ref) {
			context->hw_device_ctx = av_buffer_ref(device_ref);
			context->get_format = lj_vaapi_get_format;
			if (!context->hw_device_ctx)
				ret = AVERROR(ENOMEM);
		}
	}
	if (ret >= 0)
		ret = avcodec_open2(context, codec, NULL);
	if (ret < 0) {
		avcodec_free_context(&context);
		return ret;
	}
	*decoder = context;
	return 0;
}

int lj_video_open_decoder(LJRenderEngine *engine, int threads)
{
	LJVideoPipeline *video = &engine->video;
	const AVStream *stream = engine->input->streams[video->stream_index];
	video->in_time_base = stream->time_base;
	const AVCodec *codec = avcodec_find_decoder(stream->codecpar->codec_id);
	if (!codec)
		return AVERROR_DECODER_NOT_FOUND;
	/* Zero-copy VAAPI decode first; formats the GPU cannot decode (e.g. 10-bit H.264) use software. */
	int ret = lj_video_decoder_create(codec, stream, threads, engine->vaapi_device, &video->decoder);
	if (ret < 0)
		ret = lj_video_decoder_create(codec, stream, threads, NULL, &video->decoder);
	return ret;
}

int lj_video_send_frame(LJRenderEngine *engine, AVFrame *frame)
{
	LJVideoPipeline *video = &engine->video;
	AVCodecContext *encoder = video->encoder;
	int ret;
	do {
		ret = lj_engine_check_cancel(engine);
		if (ret < 0)
			return ret;
		ret = avcodec_send_frame(encoder, frame);
		if (ret == AVERROR(EAGAIN)) {
			int drained = lj_pipeline_receive_packets(engine, encoder, video->out_stream,
				video->packet, 0);
			if (drained < 0)
				return drained;
		}
	} while (ret == AVERROR(EAGAIN));
	if (ret < 0)
		return ret;
	video->last_pts = frame->pts;
	return lj_pipeline_receive_packets(engine, encoder, video->out_stream, video->packet, 0);
}

int lj_video_pull_filtered(LJRenderEngine *engine, int64_t pts)
{
	LJVideoPipeline *video = &engine->video;
	AVFrame *filtered = video->filtered;
	int64_t min_step = av_rescale_q(video->frame_duration_ns, LJ_NS_Q, video->encoder->time_base);
	if (min_step <= 0)
		min_step = 1;
	for (;;) {
		int ret = av_buffersink_get_frame(video->filter.sink, filtered);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
			return 0;
		if (ret < 0)
			return ret;
		if (pts != AV_NOPTS_VALUE)
			filtered->pts = pts;
		else if (video->last_pts != AV_NOPTS_VALUE && filtered->pts <= video->last_pts)
			filtered->pts = video->last_pts + min_step;
		ret = lj_video_send_frame(engine, filtered);
		av_frame_unref(filtered);
		if (ret < 0)
			return ret;
	}
}

static int lj_video_encode_frame(LJRenderEngine *engine, AVFrame *decoded, int64_t pts)
{
	LJVideoPipeline *video = &engine->video;
	int ret = lj_engine_check_cancel(engine);
	if (ret < 0)
		return ret;
	const LJFilterTarget target = {
		.width = video->encoder->width,
		.height = video->encoder->height,
		.sw_format = video->config.sw_pix_fmt,
		.time_base = video->encoder->time_base,
	};
	decoded->pts = pts;
	if (lj_filter_is_passthrough(decoded, &target))
		return lj_video_send_frame(engine, decoded);
	if (!lj_filter_matches(&video->filter, decoded)) {
		ret = lj_filter_configure(&video->filter, decoded, engine->vaapi_device, &target,
			engine->error, sizeof(engine->error));
		if (ret < 0)
			return ret;
	}
	ret = av_buffersrc_add_frame_flags(video->filter.src, decoded, AV_BUFFERSRC_FLAG_KEEP_REF);
	return ret < 0 ? ret : lj_video_pull_filtered(engine, pts);
}

static int lj_video_encode_mapped(LJRenderEngine *engine, AVFrame *frame, size_t index, int64_t source_ns)
{
	LJVideoPipeline *video = &engine->video;
	int64_t output_ns = lj_timeline_map_ns(&engine->timeline, index, source_ns);
	int64_t pts = av_rescale_q(output_ns, LJ_NS_Q, video->encoder->time_base);
	int64_t min_step = av_rescale_q(video->frame_duration_ns, LJ_NS_Q, video->encoder->time_base);
	if (min_step <= 0)
		min_step = 1;
	if (video->last_pts != AV_NOPTS_VALUE && pts <= video->last_pts)
		pts = video->last_pts + min_step;
	return lj_video_encode_frame(engine, frame, pts);
}

/* Returns 1 when the receive loop must stop (seek issued or trailing silence reached). */
static int lj_video_skip_silence(LJRenderEngine *engine, size_t index, int64_t source_ns)
{
	LJVideoPipeline *video = &engine->video;
	LJTimelineView *timeline = &engine->timeline;
	size_t next_keep;
	if (!lj_timeline_next_keep(timeline, index, &next_keep)) {
		av_log(engine->input, AV_LOG_INFO,
			"Trailing silence to EOF (%.2fs); ending video demux\n",
			(double)(timeline->source_end - source_ns) / 1e9);
		video->eof_reached = 1;
		return 1;
	}
	int64_t target_ns = timeline->entries[next_keep].segment.start_ns;
	int64_t gap_ns = target_ns - source_ns;
	if (gap_ns < LJ_MIN_SEEK_SILENCE_NS)
		return 0;
	int64_t target_pts = av_rescale_q(target_ns, LJ_NS_Q, video->in_time_base);
	av_log(engine->input, AV_LOG_INFO,
		"Seeking over %.2fs silence to target PTS %" PRId64 " (%.2fs)\n",
		(double)gap_ns / 1e9, target_pts, (double)target_ns / 1e9);
	int ret = av_seek_frame(engine->input, video->stream_index, target_pts, AVSEEK_FLAG_BACKWARD);
	if (ret < 0) {
		av_log(engine->input, AV_LOG_WARNING,
			"Seek to target PTS %" PRId64 " failed (%d); continuing linear decode\n",
			target_pts, ret);
		return 0;
	}
	avcodec_flush_buffers(video->decoder);
	video->pre_roll_target_ns = target_ns;
	video->fallback_ns = target_ns;
	timeline->cursor = next_keep;
	if (engine->audio.enabled) {
		lj_audio_seek_reset(engine->audio.decoder);
		engine->audio.pre_roll_sample = timeline->entries[next_keep].sample_start;
	}
	return 1;
}

/* Returns <0 on error, 1 to stop receiving, 0 to continue. */
static int lj_video_handle_frame(LJRenderEngine *engine, AVFrame *frame, int draining)
{
	LJVideoPipeline *video = &engine->video;
	LJTimelineView *timeline = &engine->timeline;
	int64_t source_ns = frame->best_effort_timestamp == AV_NOPTS_VALUE
		? video->fallback_ns
		: av_rescale_q(frame->best_effort_timestamp, video->in_time_base, LJ_NS_Q);
	video->fallback_ns = source_ns + video->frame_duration_ns;
	lj_progress_update(&engine->progress, source_ns, timeline->source_end);

	if (video->pre_roll_target_ns >= 0) {
		if (source_ns < video->pre_roll_target_ns)
			return 0;
		video->pre_roll_target_ns = -1;
	}
	size_t index;
	if (!lj_timeline_lookup_ns(timeline, source_ns, &index))
		return 0;
	if (timeline->entries[index].segment.action == LJ_SEGMENT_KEEP)
		return lj_video_encode_mapped(engine, frame, index, source_ns);
	if (draining)
		return 0;
	return lj_video_skip_silence(engine, index, source_ns);
}

int lj_video_receive_frames(LJRenderEngine *engine, int draining)
{
	LJVideoPipeline *video = &engine->video;
	for (;;) {
		int ret = lj_engine_check_cancel(engine);
		if (ret < 0)
			return ret;
		ret = avcodec_receive_frame(video->decoder, video->frame);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
			return 0;
		if (ret < 0)
			return ret;
		ret = lj_video_handle_frame(engine, video->frame, draining);
		av_frame_unref(video->frame);
		if (ret != 0)
			return ret < 0 ? ret : 0;
	}
}

int lj_video_feed_packet(LJRenderEngine *engine, AVPacket *packet)
{
	int ret = avcodec_send_packet(engine->video.decoder, packet);
	av_packet_unref(packet);
	return ret < 0 ? ret : lj_video_receive_frames(engine, 0);
}
