/* Audio decoder internals: decode -> normalize (48 kHz float) -> optional RNNoise -> PCM queue. */
#ifndef LJ_AUDIO_H
#define LJ_AUDIO_H

#include "../common/common.h"

#include <libavcodec/avcodec.h>
#include <libavfilter/avfilter.h>
#include <libswresample/swresample.h>

struct LJAudioDecoder {
	AVFormatContext *format; /* NULL when the render engine feeds packets */
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
	int64_t emitted_samples;

	AVFilterGraph *filter_graph; /* RNNoise, mono only */
	AVFilterContext *filter_source;
	AVFilterContext *filter_sink;
	int filter_eof_sent;
	int filter_eof;
	int64_t filter_input_samples;
	int64_t filter_target_samples;

	LJSampleBuffer pending;
	char error[LJ_ERROR_SIZE];
};

/* decoder.c */
LJAudioDecoder *lj_audio_open_stream(const AVStream *stream, int output_channels,
	LJCancelToken *cancel, int rnnoise_enabled, double rnnoise_strength,
	const char *rnnoise_model, char *error, size_t error_size);
int lj_audio_send_packet(LJAudioDecoder *decoder, const AVPacket *packet);
int lj_audio_flush(LJAudioDecoder *decoder);
/* Drops buffered audio after a demuxer seek; the next frame's PTS restarts the sample cursor. */
void lj_audio_seek_reset(LJAudioDecoder *decoder);

/* resample.c */
int lj_audio_resampler_open(LJAudioDecoder *decoder);
int lj_audio_resample_frame(LJAudioDecoder *decoder, const AVFrame *frame);
int lj_audio_resample_flush(LJAudioDecoder *decoder);
/* Queues mono or matching-channel PCM, duplicating mono to stereo when needed. */
int lj_audio_append(LJAudioDecoder *decoder, const float *samples, size_t frames, int channels);

/* rnnoise.c */
int lj_rnnoise_open(LJAudioDecoder *decoder, const char *model, double strength);
int lj_rnnoise_process(LJAudioDecoder *decoder, const float *samples, int frames);
int lj_rnnoise_finish(LJAudioDecoder *decoder);

#endif
