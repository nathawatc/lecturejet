/* Render engine lifecycle: init (open everything), run (stream), close (free everything). */
#include "pipeline.h"

#include "../hw/vaapi_device.h"

#include <stdlib.h>

static int lj_engine_alloc_buffers(LJRenderEngine *engine)
{
	engine->demux_packet = av_packet_alloc();
	engine->video.frame = av_frame_alloc();
	engine->video.filtered = av_frame_alloc();
	engine->video.packet = av_packet_alloc();
	engine->audio.packet = av_packet_alloc();
	return engine->demux_packet && engine->video.frame && engine->video.filtered &&
		engine->video.packet && engine->audio.packet ? 0 : AVERROR(ENOMEM);
}

static int lj_engine_open(LJRenderEngine *engine, const char *input, const char *output,
	const LJSegment *segments, size_t segment_count, const LJRenderOptions *options)
{
	int ret = lj_engine_check_cancel(engine);
	if (ret < 0)
		return ret;
	ret = lj_timeline_init(&engine->timeline, segments, segment_count);
	if (ret < 0) {
		lj_fferror(engine->error, sizeof(engine->error), "validating timeline", ret);
		return ret;
	}
	ret = avformat_alloc_output_context2(&engine->output, NULL, NULL, output);
	if (ret < 0 || !engine->output) {
		lj_fferror(engine->error, sizeof(engine->error), "creating output context",
			ret < 0 ? ret : AVERROR_UNKNOWN);
		return ret < 0 ? ret : AVERROR_UNKNOWN;
	}
	lj_set_interrupt_callback(engine->output, engine->cancel);

	ret = lj_engine_alloc_buffers(engine);
	if (ret >= 0)
		ret = lj_demux_open(engine, input);
	if (ret >= 0)
		ret = lj_vaapi_device_open(options->vaapi_device, &engine->vaapi_device,
			engine->error, sizeof(engine->error));
	if (ret >= 0)
		ret = lj_video_open_encoder(engine, options);
	if (ret >= 0 && engine->audio.enabled) {
		engine->audio.channels = options->output_mono ? 1 : 2;
		engine->audio.fifo.channels = engine->audio.channels;
		ret = lj_audio_pipeline_open_encoder(engine);
	}
	if (ret >= 0)
		ret = lj_video_open_decoder(engine, options->threads);
	if (ret >= 0 && engine->audio.enabled)
		ret = lj_audio_pipeline_open_decoder(engine, options);
	return ret;
}

LJRenderEngine *lj_render_init(const char *input, const char *output,
	const LJSegment *segments, size_t segment_count,
	const LJRenderOptions *options, char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	if (!input || !output || !segments || segment_count == 0 || !options || !options->video_codec) {
		lj_error(error, error_size, "rendering: invalid argument");
		return NULL;
	}
	LJRenderEngine *engine = calloc(1, sizeof(*engine));
	if (!engine) {
		lj_error(error, error_size, "rendering: out of memory");
		return NULL;
	}
	engine->cancel = options->cancel;
	lj_progress_init(&engine->progress, options->progress);
	engine->video.last_pts = AV_NOPTS_VALUE;
	engine->video.pre_roll_target_ns = -1;
	engine->audio.pre_roll_sample = -1;

	int ret = lj_engine_open(engine, input, output, segments, segment_count, options);
	if (ret < 0) {
		lj_report_failure(error, error_size, engine->error, engine->cancel, "rendering", ret);
		lj_render_close(&engine);
	}
	return engine;
}

static int lj_engine_stream(LJRenderEngine *engine)
{
	int ret = lj_engine_check_cancel(engine);
	if (ret < 0)
		return ret;
	lj_progress_report(&engine->progress, 0);
	if (!(engine->output->oformat->flags & AVFMT_NOFILE)) {
		ret = avio_open2(&engine->output->pb, engine->output->url, AVIO_FLAG_WRITE,
			&engine->output->interrupt_callback, NULL);
		if (ret < 0)
			return ret;
	}
	ret = lj_engine_check_cancel(engine);
	if (ret >= 0)
		ret = avformat_write_header(engine->output, NULL);
	if (ret >= 0)
		ret = lj_demux_run(engine);
	if (ret >= 0)
		ret = lj_drain_all(engine);
	if (ret >= 0 && !(engine->output->oformat->flags & AVFMT_NOFILE)) {
		ret = avio_closep(&engine->output->pb);
		if (ret < 0)
			lj_fferror(engine->error, sizeof(engine->error), "closing output", ret);
	}
	return ret;
}

int lj_render_run(LJRenderEngine *engine, char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	if (!engine) {
		lj_error(error, error_size, "rendering: invalid argument");
		return AVERROR(EINVAL);
	}
	int ret = lj_engine_stream(engine);
	if (ret < 0)
		lj_report_failure(error, error_size, engine->error, engine->cancel, "rendering", ret);
	return ret;
}

void lj_render_close(LJRenderEngine **engine_ptr)
{
	if (!engine_ptr || !*engine_ptr)
		return;
	LJRenderEngine *engine = *engine_ptr;
	LJVideoPipeline *video = &engine->video;
	LJAudioPipeline *audio = &engine->audio;

	lj_filter_free(&video->filter);
	av_frame_free(&video->frame);
	av_frame_free(&video->filtered);
	av_packet_free(&video->packet);
	avcodec_free_context(&video->decoder);
	avcodec_free_context(&video->encoder);

	lj_audio_close(&audio->decoder);
	av_packet_free(&audio->packet);
	swr_free(&audio->resampler);
	avcodec_free_context(&audio->encoder);
	lj_samples_free(&audio->fifo);
	free(audio->chunk);

	av_packet_free(&engine->demux_packet);
	avformat_close_input(&engine->input);
	if (engine->output) {
		if (!(engine->output->oformat->flags & AVFMT_NOFILE))
			avio_closep(&engine->output->pb);
		avformat_free_context(engine->output);
	}
	av_buffer_unref(&engine->vaapi_device);
	lj_timeline_free(&engine->timeline);
	free(engine);
	*engine_ptr = NULL;
}
