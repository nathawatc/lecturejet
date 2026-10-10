/* End-of-stream: write encoder packets, flush every stage in order, finish the container. */
#include "pipeline.h"

#include <libavfilter/buffersrc.h>

static int lj_write_packet(LJRenderEngine *engine, AVPacket *packet, AVStream *stream,
	AVRational source_time_base)
{
	int ret = lj_engine_check_cancel(engine);
	if (ret < 0)
		return ret;
	packet->stream_index = stream->index;
	av_packet_rescale_ts(packet, source_time_base, stream->time_base);
	if (packet->dts != AV_NOPTS_VALUE && packet->dts < 0)
		packet->dts = 0;
	if (packet->pts != AV_NOPTS_VALUE && packet->pts < 0)
		packet->pts = 0;
	if (stream == engine->video.out_stream) {
		LJVideoPipeline *video = &engine->video;
		if (packet->dts != AV_NOPTS_VALUE) {
			if (video->last_dts != AV_NOPTS_VALUE && packet->dts <= video->last_dts)
				packet->dts = video->last_dts + 1;
			video->last_dts = packet->dts;
		}
		if (packet->pts != AV_NOPTS_VALUE && packet->pts < packet->dts)
			packet->pts = packet->dts;
	}
	ret = av_interleaved_write_frame(engine->output, packet);
	if (ret < 0)
		lj_fferror(engine->error, sizeof(engine->error), "writing output packet", ret);
	return ret;
}

int lj_pipeline_receive_packets(LJRenderEngine *engine, AVCodecContext *encoder,
	AVStream *stream, AVPacket *packet, int64_t duration_override)
{
	for (;;) {
		int ret = lj_engine_check_cancel(engine);
		if (ret < 0)
			return ret;
		ret = avcodec_receive_packet(encoder, packet);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
			return 0;
		if (ret < 0)
			return ret;
		if (duration_override > 0)
			packet->duration = duration_override;
		ret = lj_write_packet(engine, packet, stream, encoder->time_base);
		av_packet_unref(packet);
		if (ret < 0)
			return ret;
	}
}

static int lj_flush_encoder(LJRenderEngine *engine, AVCodecContext *encoder, AVStream *stream,
	AVPacket *packet)
{
	int ret = lj_engine_check_cancel(engine);
	if (ret < 0)
		return ret;
	ret = avcodec_send_frame(encoder, NULL);
	if (ret < 0 && ret != AVERROR_EOF)
		return ret;
	return lj_pipeline_receive_packets(engine, encoder, stream, packet, 0);
}

static int lj_drain_video_decoder(LJRenderEngine *engine)
{
	int ret = avcodec_send_packet(engine->video.decoder, NULL);
	if (ret < 0 && ret != AVERROR_EOF)
		return ret;
	return lj_video_receive_frames(engine, 1);
}

static int lj_drain_audio(LJRenderEngine *engine)
{
	LJAudioPipeline *audio = &engine->audio;
	int ret = lj_audio_flush(audio->decoder);
	if (ret >= 0)
		ret = lj_audio_pipeline_process(engine);
	if (ret >= 0)
		ret = lj_audio_pipeline_encode_available(engine, 1);
	if (ret >= 0)
		ret = lj_flush_encoder(engine, audio->encoder, audio->out_stream, audio->packet);
	return ret;
}

static int lj_drain_filter(LJRenderEngine *engine)
{
	if (!engine->video.filter.src)
		return 0;
	int ret = av_buffersrc_add_frame_flags(engine->video.filter.src, NULL, 0);
	if (ret < 0 && ret != AVERROR_EOF)
		return ret;
	return lj_video_pull_filtered(engine, AV_NOPTS_VALUE);
}

int lj_drain_all(LJRenderEngine *engine)
{
	LJVideoPipeline *video = &engine->video;
	int ret = lj_drain_video_decoder(engine);
	if (ret >= 0 && engine->audio.enabled)
		ret = lj_drain_audio(engine);
	if (ret >= 0)
		ret = lj_drain_filter(engine);
	if (ret >= 0)
		ret = lj_flush_encoder(engine, video->encoder, video->out_stream, video->packet);
	if (ret >= 0)
		ret = av_write_trailer(engine->output);
	if (ret >= 0)
		lj_progress_report(&engine->progress, 1000);
	return ret;
}
