#include "pipeline.h"

int lj_demux_open(LJRenderEngine *engine, const char *path)
{
	engine->input = avformat_alloc_context();
	if (!engine->input)
		return AVERROR(ENOMEM);
	lj_set_interrupt_callback(engine->input, engine->cancel);
	int ret = avformat_open_input(&engine->input, path, NULL, NULL);
	if (ret >= 0)
		ret = avformat_find_stream_info(engine->input, NULL);
	if (ret < 0)
		return ret;

	engine->video.stream_index = av_find_best_stream(engine->input, AVMEDIA_TYPE_VIDEO,
		-1, -1, NULL, 0);
	if (engine->video.stream_index < 0) {
		lj_error(engine->error, sizeof(engine->error), "rendering: input has no video stream");
		return AVERROR_STREAM_NOT_FOUND;
	}
	engine->audio.stream_index = av_find_best_stream(engine->input, AVMEDIA_TYPE_AUDIO,
		-1, -1, NULL, 0);
	engine->audio.enabled = engine->audio.stream_index >= 0;
	return 0;
}

int lj_demux_run(LJRenderEngine *engine)
{
	AVPacket *packet = engine->demux_packet;
	while (!engine->video.eof_reached) {
		int ret = lj_engine_check_cancel(engine);
		if (ret < 0)
			return ret;
		ret = av_read_frame(engine->input, packet);
		if (ret == AVERROR_EOF)
			return 0;
		if (ret < 0)
			return ret;
		if (packet->stream_index == engine->video.stream_index)
			ret = lj_video_feed_packet(engine, packet);
		else if (engine->audio.enabled && packet->stream_index == engine->audio.stream_index)
			ret = lj_audio_pipeline_feed_packet(engine, packet);
		else
			av_packet_unref(packet);
		if (ret < 0)
			return ret;
	}
	return 0;
}
