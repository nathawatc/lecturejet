#include "audio.h"

#include <libavutil/mathematics.h>
#include <math.h>
#include <stdlib.h>
#include <string.h>

static int lj_audio_process_frame(LJAudioDecoder *decoder)
{
	const AVFrame *frame = decoder->frame;
	if (decoder->emitted_samples < 0 && frame->pts != AV_NOPTS_VALUE)
		decoder->emitted_samples = av_rescale_q(frame->pts, decoder->codec->pkt_timebase, LJ_SAMPLE_Q);
	int ret = lj_audio_resample_frame(decoder, frame);
	av_frame_unref(decoder->frame);
	return ret;
}

static int lj_audio_receive_all(LJAudioDecoder *decoder)
{
	for (;;) {
		if (lj_cancel_requested(decoder->cancel))
			return AVERROR_EXIT;
		int ret = avcodec_receive_frame(decoder->codec, decoder->frame);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
			return 0;
		if (ret >= 0)
			ret = lj_audio_process_frame(decoder);
		if (ret < 0)
			return ret;
	}
}

/* Flushes the resampler and the RNNoise graph once the codec has no more frames. */
static int lj_audio_finish(LJAudioDecoder *decoder)
{
	int ret = lj_audio_resample_flush(decoder);
	if (ret >= 0 && decoder->filter_source)
		ret = lj_rnnoise_finish(decoder);
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
		if (ret >= 0) {
			ret = lj_audio_process_frame(decoder);
			if (ret < 0 || decoder->pending.frames > 0)
				return ret;
			continue;
		}
		if (ret == AVERROR_EOF) {
			decoder->decoder_eof = 1;
			return lj_audio_finish(decoder);
		}
		if (ret != AVERROR(EAGAIN))
			return ret;

		if (!decoder->input_eof) {
			ret = av_read_frame(decoder->format, decoder->packet);
			if (ret == AVERROR_EOF) {
				decoder->input_eof = 1;
			} else if (ret < 0) {
				return ret;
			} else {
				ret = decoder->packet->stream_index == decoder->stream_index
					? avcodec_send_packet(decoder->codec, decoder->packet) : 0;
				av_packet_unref(decoder->packet);
				if (ret < 0)
					return ret;
				continue;
			}
		}
		if (decoder->decoder_flush_sent)
			return AVERROR_BUG;
		ret = avcodec_send_packet(decoder->codec, NULL);
		if (ret < 0 && ret != AVERROR_EOF)
			return ret;
		decoder->decoder_flush_sent = 1;
	}
}

static int lj_audio_init_codec(LJAudioDecoder *decoder, const AVStream *stream,
	int rnnoise_enabled, double rnnoise_strength, const char *rnnoise_model)
{
	const AVCodec *codec = avcodec_find_decoder(stream->codecpar->codec_id);
	if (!codec) {
		lj_error(decoder->error, sizeof(decoder->error), "finding audio decoder");
		return AVERROR_DECODER_NOT_FOUND;
	}
	decoder->codec = avcodec_alloc_context3(codec);
	if (!decoder->codec)
		return AVERROR(ENOMEM);
	int ret = avcodec_parameters_to_context(decoder->codec, stream->codecpar);
	if (ret < 0)
		return ret;
	decoder->codec->pkt_timebase = stream->time_base;
	decoder->codec->thread_count = 1;
	ret = avcodec_open2(decoder->codec, codec, NULL);
	if (ret < 0)
		return ret;
	decoder->packet = av_packet_alloc();
	decoder->frame = av_frame_alloc();
	if (!decoder->packet || !decoder->frame)
		return AVERROR(ENOMEM);
	if (rnnoise_enabled) {
		ret = lj_rnnoise_open(decoder, rnnoise_model, rnnoise_strength);
		if (ret < 0)
			return ret;
	}
	return lj_audio_resampler_open(decoder);
}

static LJAudioDecoder *lj_audio_alloc(int output_channels, LJCancelToken *cancel,
	int rnnoise_enabled, const char *rnnoise_model, char *error, size_t error_size)
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
	decoder->pending.channels = output_channels;
	decoder->cancel = cancel;
	return decoder;
}

LJAudioDecoder *lj_audio_open_stream(const AVStream *stream, int output_channels,
	LJCancelToken *cancel, int rnnoise_enabled, double rnnoise_strength,
	const char *rnnoise_model, char *error, size_t error_size)
{
	LJAudioDecoder *decoder = lj_audio_alloc(output_channels, cancel, rnnoise_enabled,
		rnnoise_model, error, error_size);
	if (!decoder)
		return NULL;
	decoder->stream_index = stream->index;
	int ret = lj_audio_init_codec(decoder, stream, rnnoise_enabled, rnnoise_strength, rnnoise_model);
	if (ret < 0) {
		lj_report_failure(error, error_size, decoder->error, cancel, "opening audio decoder", ret);
		lj_audio_close(&decoder);
	}
	return decoder;
}

static int lj_audio_open_input(LJAudioDecoder *decoder, const char *path)
{
	decoder->format = avformat_alloc_context();
	if (!decoder->format)
		return AVERROR(ENOMEM);
	lj_set_interrupt_callback(decoder->format, decoder->cancel);
	int ret = avformat_open_input(&decoder->format, path, NULL, NULL);
	if (ret >= 0)
		ret = avformat_find_stream_info(decoder->format, NULL);
	if (ret < 0)
		return ret;
	decoder->stream_index = av_find_best_stream(decoder->format, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);
	return decoder->stream_index < 0 ? AVERROR_STREAM_NOT_FOUND : 0;
}

LJAudioDecoder *lj_audio_open(const char *path, int output_channels,
	LJCancelToken *cancel, int rnnoise_enabled, double rnnoise_strength,
	const char *rnnoise_model, char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	LJAudioDecoder *decoder = lj_audio_alloc(output_channels, cancel, rnnoise_enabled,
		rnnoise_model, error, error_size);
	if (!decoder)
		return NULL;
	int ret = lj_audio_open_input(decoder, path);
	if (ret >= 0)
		ret = lj_audio_init_codec(decoder, decoder->format->streams[decoder->stream_index],
			rnnoise_enabled, rnnoise_strength, rnnoise_model);
	if (ret < 0) {
		lj_report_failure(error, error_size, decoder->error, cancel, "opening audio decoder", ret);
		lj_audio_close(&decoder);
	}
	return decoder;
}

int lj_audio_send_packet(LJAudioDecoder *decoder, const AVPacket *packet)
{
	int ret = avcodec_send_packet(decoder->codec, packet);
	return ret < 0 ? ret : lj_audio_receive_all(decoder);
}

int lj_audio_flush(LJAudioDecoder *decoder)
{
	int ret = avcodec_send_packet(decoder->codec, NULL);
	if (ret < 0 && ret != AVERROR_EOF)
		return ret;
	ret = lj_audio_receive_all(decoder);
	return ret < 0 ? ret : lj_audio_finish(decoder);
}

void lj_audio_seek_reset(LJAudioDecoder *decoder)
{
	avcodec_flush_buffers(decoder->codec);
	swr_close(decoder->resampler);
	swr_init(decoder->resampler);
	decoder->pending.frames = 0;
	decoder->swr_flushed = 0;
	decoder->emitted_samples = -1;
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
	while (decoder->format && !decoder->decoder_eof && decoder->pending.frames < (size_t)max_samples) {
		int ret = lj_audio_decode_more(decoder);
		if (ret < 0) {
			lj_report_failure(error, error_size, decoder->error, decoder->cancel, "decoding audio", ret);
			return -1;
		}
	}
	if (start_sample)
		*start_sample = decoder->emitted_samples;
	size_t frames = decoder->pending.frames < (size_t)max_samples
		? decoder->pending.frames : (size_t)max_samples;
	size_t values = frames * (size_t)decoder->output_channels;
	for (size_t i = 0; i < values; ++i) {
		if (!isfinite(decoder->pending.data[i])) {
			lj_error(error, error_size, "decoding audio: non-finite PCM sample at index %zu", i);
			return -1;
		}
	}
	if (frames > 0) {
		memcpy(output, decoder->pending.data, values * sizeof(float));
		lj_samples_discard(&decoder->pending, frames);
		decoder->emitted_samples += (int64_t)frames;
	}
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
	lj_samples_free(&decoder->pending);
	free(decoder);
	*decoder_ptr = NULL;
}
