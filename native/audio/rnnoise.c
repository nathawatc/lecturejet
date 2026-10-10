#include "audio.h"

#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libavutil/channel_layout.h>
#include <libavutil/opt.h>
#include <stdio.h>
#include <string.h>

/* arnndn works on 10 ms frames at 48 kHz. */
#define LJ_RNNOISE_FRAME_SAMPLES 480

static int lj_rnnoise_find(const char *name, const AVFilter **filter)
{
	*filter = avfilter_get_by_name(name);
	return *filter ? 0 : AVERROR_FILTER_NOT_FOUND;
}

int lj_rnnoise_open(LJAudioDecoder *decoder, const char *model, double strength)
{
	const AVFilter *buffer_source, *buffer_sink, *frame_filter, *denoise;
	int ret = lj_rnnoise_find("abuffer", &buffer_source);
	if (ret >= 0)
		ret = lj_rnnoise_find("abuffersink", &buffer_sink);
	if (ret >= 0)
		ret = lj_rnnoise_find("asetnsamples", &frame_filter);
	if (ret >= 0)
		ret = lj_rnnoise_find("arnndn", &denoise);
	if (ret < 0)
		return ret;
	AVFilterGraph *graph = avfilter_graph_alloc();
	if (!graph)
		return AVERROR(ENOMEM);
	decoder->filter_graph = graph;

	char frame_args[32];
	snprintf(frame_args, sizeof(frame_args), "n=%d:pad=1", LJ_RNNOISE_FRAME_SAMPLES);
	char source_args[128];
	snprintf(source_args, sizeof(source_args),
		"time_base=1/%d:sample_rate=%d:sample_fmt=flt:channel_layout=mono",
		LJ_SAMPLE_RATE, LJ_SAMPLE_RATE);

	AVFilterContext *noise = NULL;
	AVFilterContext *frames = NULL;
	ret = avfilter_graph_create_filter(&decoder->filter_source, buffer_source, "input",
		source_args, NULL, graph);
	if (ret >= 0) {
		noise = avfilter_graph_alloc_filter(graph, denoise, "rnnoise");
		ret = noise ? av_opt_set(noise, "model", model, AV_OPT_SEARCH_CHILDREN) : AVERROR(ENOMEM);
	}
	if (ret >= 0)
		ret = av_opt_set_double(noise, "mix", strength, AV_OPT_SEARCH_CHILDREN);
	if (ret >= 0)
		ret = avfilter_init_str(noise, NULL);
	if (ret >= 0)
		ret = avfilter_graph_create_filter(&frames, frame_filter, "frames", frame_args, NULL, graph);
	if (ret >= 0)
		ret = avfilter_graph_create_filter(&decoder->filter_sink, buffer_sink, "output",
			NULL, NULL, graph);
	if (ret >= 0)
		ret = avfilter_link(decoder->filter_source, 0, frames, 0);
	if (ret >= 0)
		ret = avfilter_link(frames, 0, noise, 0);
	if (ret >= 0)
		ret = avfilter_link(noise, 0, decoder->filter_sink, 0);
	return ret < 0 ? ret : avfilter_graph_config(graph, NULL);
}

static int lj_rnnoise_feed(LJAudioDecoder *decoder, const float *samples, int frames)
{
	AVFrame *input = av_frame_alloc();
	if (!input)
		return AVERROR(ENOMEM);
	input->format = AV_SAMPLE_FMT_FLT;
	input->sample_rate = LJ_SAMPLE_RATE;
	input->nb_samples = frames;
	input->pts = decoder->filter_input_samples;
	av_channel_layout_default(&input->ch_layout, 1);
	int ret = av_frame_get_buffer(input, 0);
	if (ret >= 0) {
		memcpy(input->data[0], samples, (size_t)frames * sizeof(float));
		ret = av_buffersrc_add_frame_flags(decoder->filter_source, input, AV_BUFFERSRC_FLAG_KEEP_REF);
	}
	if (ret >= 0)
		decoder->filter_input_samples += frames;
	av_frame_free(&input);
	return ret;
}

/* Pulls every ready 10 ms frame, trimming the zero padding added after the last input sample. */
static int lj_rnnoise_drain(LJAudioDecoder *decoder)
{
	AVFrame *filtered = av_frame_alloc();
	if (!filtered)
		return AVERROR(ENOMEM);
	int ret = 0;
	while (!decoder->filter_eof) {
		ret = av_buffersink_get_samples(decoder->filter_sink, filtered, LJ_RNNOISE_FRAME_SAMPLES);
		if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
			decoder->filter_eof = ret == AVERROR_EOF;
			ret = 0;
			break;
		}
		if (ret < 0) {
			lj_fferror(decoder->error, sizeof(decoder->error), "reading RNNoise output", ret);
			break;
		}
		int valid = filtered->nb_samples;
		if (decoder->filter_target_samples > 0) {
			int64_t remaining = decoder->filter_target_samples - decoder->emitted_samples -
				(int64_t)decoder->pending.frames;
			valid = remaining <= 0 ? 0 : remaining < valid ? (int)remaining : valid;
		}
		ret = lj_audio_append(decoder, (const float *)filtered->data[0], (size_t)valid, 1);
		av_frame_unref(filtered);
		if (ret < 0) {
			lj_fferror(decoder->error, sizeof(decoder->error), "buffering RNNoise output", ret);
			break;
		}
	}
	av_frame_free(&filtered);
	return ret;
}

int lj_rnnoise_process(LJAudioDecoder *decoder, const float *samples, int frames)
{
	int ret = lj_rnnoise_feed(decoder, samples, frames);
	return ret < 0 ? ret : lj_rnnoise_drain(decoder);
}

int lj_rnnoise_finish(LJAudioDecoder *decoder)
{
	if (decoder->filter_eof_sent)
		return 0;
	int ret = av_buffersrc_close(decoder->filter_source, decoder->filter_input_samples, 0);
	if (ret < 0)
		return ret;
	decoder->filter_target_samples = decoder->filter_input_samples;
	decoder->filter_eof_sent = 1;
	return lj_rnnoise_drain(decoder);
}
