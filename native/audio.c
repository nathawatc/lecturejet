#include "internal.h"

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
