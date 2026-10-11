/* Audio path: decode, timeline routing, FIFO, AAC encode. */
#include "pipeline.h"

#include <libavutil/channel_layout.h>
#include <stdlib.h>
#include <string.h>

/* Decoded PCM staging block, in frames per channel. */
#define LJ_AUDIO_CHUNK_FRAMES 8192

int lj_audio_pipeline_open_encoder(LJRenderEngine *engine)
{
	LJAudioPipeline *audio = &engine->audio;
	const LJAudioConfig *config = lj_audio_profile();
	int ret = lj_audio_encoder_open(config, audio->channels,
		engine->output->oformat->flags & AVFMT_GLOBALHEADER, &audio->encoder);
	if (ret < 0)
		return ret;
	audio->frame_size = audio->encoder->frame_size > 0
		? audio->encoder->frame_size : config->default_frame_size;
	audio->out_stream = avformat_new_stream(engine->output, NULL);
	if (!audio->out_stream)
		return AVERROR(ENOMEM);
	ret = avcodec_parameters_from_context(audio->out_stream->codecpar, audio->encoder);
	if (ret < 0)
		return ret;
	audio->out_stream->time_base = audio->encoder->time_base;

	AVChannelLayout input_layout;
	av_channel_layout_default(&input_layout, audio->channels);
	ret = swr_alloc_set_opts2(&audio->resampler, &audio->encoder->ch_layout,
		audio->encoder->sample_fmt, config->sample_rate, &input_layout,
		AV_SAMPLE_FMT_FLT, config->sample_rate, 0, NULL);
	return ret < 0 ? ret : swr_init(audio->resampler);
}

int lj_audio_pipeline_open_decoder(LJRenderEngine *engine, const LJRenderOptions *options)
{
	LJAudioPipeline *audio = &engine->audio;
	audio->decoder = lj_audio_open_stream(engine->input->streams[audio->stream_index],
		audio->channels, engine->cancel, options->rnnoise_enabled, options->rnnoise_strength,
		options->rnnoise_model, engine->error, sizeof(engine->error));
	if (!audio->decoder)
		return AVERROR_EXTERNAL;
	audio->chunk = malloc((size_t)LJ_AUDIO_CHUNK_FRAMES * audio->channels * sizeof(float));
	return audio->chunk ? 0 : AVERROR(ENOMEM);
}

/* Encodes one AAC frame from the FIFO head; a short tail is zero padded. */
static int lj_audio_encode_frame(LJRenderEngine *engine, size_t valid)
{
	LJAudioPipeline *audio = &engine->audio;
	size_t frame_size = (size_t)audio->frame_size;
	AVFrame *frame = av_frame_alloc();
	if (!frame)
		return AVERROR(ENOMEM);
	frame->nb_samples = audio->frame_size;
	frame->format = audio->encoder->sample_fmt;
	frame->sample_rate = audio->encoder->sample_rate;
	frame->pts = audio->written;
	int ret = av_channel_layout_copy(&frame->ch_layout, &audio->encoder->ch_layout);
	if (ret >= 0)
		ret = av_frame_get_buffer(frame, 0);
	if (ret >= 0) {
		const float *input = audio->fifo.data;
		float *padded = NULL;
		if (valid < frame_size) {
			padded = calloc(frame_size * (size_t)audio->channels, sizeof(float));
			if (!padded)
				ret = AVERROR(ENOMEM);
			else {
				memcpy(padded, input, valid * (size_t)audio->channels * sizeof(float));
				input = padded;
			}
		}
		if (ret >= 0) {
			const uint8_t *input_data[1] = {(const uint8_t *)input};
			ret = swr_convert(audio->resampler, frame->data, audio->frame_size,
				input_data, audio->frame_size);
		}
		free(padded);
	}
	if (ret >= 0) {
		ret = avcodec_send_frame(audio->encoder, frame);
		if (ret == AVERROR(EAGAIN))
			ret = AVERROR_BUG;
	}
	av_frame_free(&frame);
	return ret < 0 ? ret : 0;
}

int lj_audio_pipeline_encode_available(LJRenderEngine *engine, int flush_partial)
{
	LJAudioPipeline *audio = &engine->audio;
	size_t frame_size = (size_t)audio->frame_size;
	while (audio->fifo.frames >= frame_size || (flush_partial && audio->fifo.frames > 0)) {
		int ret = lj_engine_check_cancel(engine);
		if (ret < 0)
			return ret;
		size_t valid = audio->fifo.frames < frame_size ? audio->fifo.frames : frame_size;
		ret = lj_audio_encode_frame(engine, valid);
		if (ret >= 0)
			ret = lj_pipeline_receive_packets(engine, audio->encoder, audio->out_stream,
				audio->packet, valid < frame_size ? (int64_t)valid : 0);
		if (ret < 0)
			return ret;
		audio->written += (int64_t)valid;
		lj_samples_discard(&audio->fifo, valid);
		if (valid < frame_size)
			break;
	}
	return 0;
}

/* Routes one decoded block through the timeline, keeping only samples in KEEP segments. */
static int lj_audio_route_block(LJRenderEngine *engine, int frames, int64_t source_sample)
{
	LJAudioPipeline *audio = &engine->audio;
	LJTimelineView *timeline = &engine->timeline;
	int offset = 0;
	while (offset < frames) {
		int ret = lj_engine_check_cancel(engine);
		if (ret < 0)
			return ret;
		if (audio->pre_roll_sample >= 0) {
			if (source_sample < audio->pre_roll_sample) {
				int64_t skip = audio->pre_roll_sample - source_sample;
				if (skip > frames - offset)
					skip = frames - offset;
				offset += (int)skip;
				source_sample += skip;
				continue;
			}
			audio->pre_roll_sample = -1;
		}
		size_t index;
		if (!lj_timeline_lookup_sample(timeline, source_sample, &index))
			return 0;
		int64_t take = timeline->entries[index].sample_end - source_sample;
		if (take > frames - offset)
			take = frames - offset;
		if (timeline->entries[index].segment.action == LJ_SEGMENT_KEEP) {
			float *tail = lj_samples_extend(&audio->fifo, (size_t)take);
			if (!tail)
				return AVERROR(ENOMEM);
			memcpy(tail, audio->chunk + (size_t)offset * audio->channels,
				(size_t)take * audio->channels * sizeof(float));
			ret = lj_audio_pipeline_encode_available(engine, 0);
			if (ret < 0)
				return ret;
		}
		offset += (int)take;
		source_sample += take;
	}
	return 0;
}

int lj_audio_pipeline_process(LJRenderEngine *engine)
{
	LJAudioPipeline *audio = &engine->audio;
	for (;;) {
		int ret = lj_engine_check_cancel(engine);
		if (ret < 0)
			return ret;
		int64_t block_start = 0;
		int frames = lj_audio_read(audio->decoder, audio->chunk, LJ_AUDIO_CHUNK_FRAMES,
			&block_start, engine->error, sizeof(engine->error));
		if (frames < 0)
			return AVERROR_EXTERNAL;
		if (frames == 0)
			return 0;
		ret = lj_audio_route_block(engine, frames, block_start);
		if (ret < 0)
			return ret;
	}
}

int lj_audio_pipeline_feed_packet(LJRenderEngine *engine, AVPacket *packet)
{
	int ret = lj_audio_send_packet(engine->audio.decoder, packet);
	av_packet_unref(packet);
	return ret < 0 ? ret : lj_audio_pipeline_process(engine);
}
