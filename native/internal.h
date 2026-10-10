#ifndef LJ_INTERNAL_H
#define LJ_INTERNAL_H

#include "lecturejet_ffmpeg.h"

#include <math.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libavutil/avassert.h>
#include <libavutil/channel_layout.h>
#include <libavutil/error.h>
#include <libavutil/hwcontext.h>
#include <libavutil/imgutils.h>
#include <libavutil/mathematics.h>
#include <libavutil/opt.h>
#include <libavutil/pixdesc.h>
#include <libavutil/samplefmt.h>
#include <libswresample/swresample.h>
#include <libswscale/swscale.h>

#define LJ_DEFAULT_VIDEO_QP 28
#define LJ_MIN_SEEK_SILENCE_SEC 3.0
#define LJ_MIN_SEEK_SILENCE_NS ((int64_t)(LJ_MIN_SEEK_SILENCE_SEC * 1000000000.0))

struct LJCancelToken {
	int cancelled;
};

typedef struct LJTimelineView {
	const LJSegment *segments;
	size_t count;
	int64_t *output_start;
	int64_t *sample_start;
	int64_t *sample_end;
	int64_t source_end;
	size_t cursor;
} LJTimelineView;

void lj_error(char *error, size_t error_size, const char *format, ...);
void lj_fferror(char *error, size_t error_size, const char *operation, int code);
int lj_cancel_requested(const LJCancelToken *token);
int lj_abort_if_cancelled(LJCancelToken *token, char *error, size_t error_size);
void lj_set_interrupt_callback(AVFormatContext *format, LJCancelToken *token);
uint32_t lj_fourcc(const char tag[4]);
void lj_report_progress(LJProgressCallback callback, int percent, int *last_percent);
int64_t lj_stream_duration_ns(const AVStream *stream);
int lj_set_video_quality(AVCodecContext *context, char *error, size_t error_size);
int lj_alloc_vaapi_frames(AVBufferRef *device_ref, enum AVPixelFormat sw_format,
	int width, int height, int pool_size, AVBufferRef **frames_ref_out);

void lj_timeline_free(LJTimelineView *view);
int lj_timeline_init(LJTimelineView *view, const LJSegment *segments, size_t count);
int lj_timeline_lookup_ns(LJTimelineView *view, int64_t source_ns, size_t *index);
int lj_timeline_lookup_sample(LJTimelineView *view, int64_t sample, size_t *index);
int64_t lj_timeline_map_ns(const LJTimelineView *view, size_t index, int64_t source_ns);
int lj_timeline_next_keep(const LJTimelineView *view, size_t from_index, size_t *next_keep_index);

LJAudioDecoder *lj_audio_open_stream(const AVStream *stream, int output_channels,
	LJCancelToken *cancel,
	int rnnoise_enabled, double rnnoise_strength,
	const char *rnnoise_model, char *error, size_t error_size);
int lj_audio_send_packet(LJAudioDecoder *decoder, const AVPacket *packet);
int lj_audio_flush(LJAudioDecoder *decoder);
void lj_audio_seek_reset(LJAudioDecoder *decoder, int64_t target_sample);

#endif
