#include "audio.h"

#include <libavutil/channel_layout.h>
#include <stdlib.h>
#include <string.h>

#define LJ_FLUSH_CHUNK_FRAMES 8192
/* swr_get_out_samples can under-report while buffering; leave room for the delay. */
#define LJ_RESAMPLE_SLACK_FRAMES 64

int lj_audio_append(LJAudioDecoder *decoder, const float *samples, size_t frames, int channels)
{
	if (frames == 0)
		return 0;
	if (channels <= 0 || channels > 2)
		return AVERROR(EINVAL);
	int output_channels = decoder->output_channels;
	if (channels != output_channels && !(channels == 1 && output_channels == 2))
		return AVERROR(EINVAL);
	float *destination = lj_samples_extend(&decoder->pending, frames);
	if (!destination)
		return AVERROR(ENOMEM);
	if (channels == output_channels) {
		memcpy(destination, samples, frames * (size_t)channels * sizeof(float));
		return 0;
	}
	for (size_t i = 0; i < frames; ++i) {
		destination[i * 2] = samples[i];
		destination[i * 2 + 1] = samples[i];
	}
	return 0;
}

int lj_audio_resampler_open(LJAudioDecoder *decoder)
{
	const AVCodecContext *codec = decoder->codec;
	AVChannelLayout input_layout = codec->ch_layout;
	if (input_layout.nb_channels <= 0)
		av_channel_layout_default(&input_layout, 1);
	AVChannelLayout output_layout;
	av_channel_layout_default(&output_layout, decoder->filter_source ? 1 : decoder->output_channels);
	int ret = swr_alloc_set_opts2(&decoder->resampler, &output_layout, AV_SAMPLE_FMT_FLT,
		LJ_SAMPLE_RATE, &input_layout, codec->sample_fmt, codec->sample_rate, 0, NULL);
	av_channel_layout_uninit(&output_layout);
	return ret < 0 ? ret : swr_init(decoder->resampler);
}

static int lj_audio_deliver(LJAudioDecoder *decoder, const float *samples, int frames)
{
	if (frames <= 0)
		return 0;
	if (decoder->filter_source)
		return lj_rnnoise_process(decoder, samples, frames);
	return lj_audio_append(decoder, samples, (size_t)frames, decoder->output_channels);
}

int lj_audio_resample_frame(LJAudioDecoder *decoder, const AVFrame *frame)
{
	if (lj_cancel_requested(decoder->cancel))
		return AVERROR_EXIT;
	int capacity = swr_get_out_samples(decoder->resampler, frame->nb_samples);
	if (capacity < 1)
		capacity = frame->nb_samples + LJ_RESAMPLE_SLACK_FRAMES;
	float *converted = malloc((size_t)capacity * decoder->output_channels * sizeof(float));
	if (!converted)
		return AVERROR(ENOMEM);
	uint8_t *output[1] = {(uint8_t *)converted};
	int converted_frames = swr_convert(decoder->resampler, output, capacity,
		(const uint8_t **)frame->extended_data, frame->nb_samples);
	int ret = lj_audio_deliver(decoder, converted, converted_frames);
	free(converted);
	return converted_frames < 0 ? converted_frames : ret;
}

int lj_audio_resample_flush(LJAudioDecoder *decoder)
{
	if (decoder->swr_flushed)
		return 0;
	for (;;) {
		if (lj_cancel_requested(decoder->cancel))
			return AVERROR_EXIT;
		int capacity = swr_get_out_samples(decoder->resampler, 0);
		if (capacity <= 0)
			break;
		if (capacity > LJ_FLUSH_CHUNK_FRAMES)
			capacity = LJ_FLUSH_CHUNK_FRAMES;
		float *converted = malloc((size_t)capacity * decoder->output_channels * sizeof(float));
		if (!converted)
			return AVERROR(ENOMEM);
		uint8_t *output[1] = {(uint8_t *)converted};
		int converted_frames = swr_convert(decoder->resampler, output, capacity, NULL, 0);
		int ret = converted_frames < 0 ? converted_frames
			: lj_audio_deliver(decoder, converted, converted_frames);
		free(converted);
		if (ret < 0)
			return ret;
		if (converted_frames == 0)
			break;
	}
	decoder->swr_flushed = 1;
	return 0;
}
