/* Internal pipeline state shared by native/pipeline C files. Never included by Go. */
#ifndef LJ_TYPES_H
#define LJ_TYPES_H

#include "lecturejet.h"
#include "../audio/audio.h"
#include "../common/common.h"
#include "../config/encoder_profile.h"
#include "../filter/filtergraph.h"
#include "../timeline/timeline.h"

#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libswresample/swresample.h>

typedef struct LJVideoPipeline {
	int stream_index;
	AVRational in_time_base;
	AVStream *out_stream;
	AVCodecContext *decoder;
	AVCodecContext *encoder;
	LJVideoConfig config;
	LJFilterGraph filter;
	AVFrame *frame;    /* decoded */
	AVFrame *filtered; /* graph output */
	AVPacket *packet;  /* encoder output */
	int64_t last_pts;  /* last encoder PTS, AV_NOPTS_VALUE before the first frame */
	int64_t last_dts;  /* last output DTS, AV_NOPTS_VALUE before the first packet */
	int64_t fallback_ns;
	int64_t frame_duration_ns;
	int64_t pre_roll_target_ns; /* drop frames before this after a seek, -1 when idle */
	int eof_reached;            /* trailing silence reached: stop demuxing */
} LJVideoPipeline;

typedef struct LJAudioPipeline {
	int enabled;
	int stream_index;
	int channels;
	int frame_size;
	AVStream *out_stream;
	LJAudioDecoder *decoder;
	AVCodecContext *encoder;
	SwrContext *resampler; /* interleaved float -> encoder sample format */
	AVPacket *packet;
	LJSampleBuffer fifo;
	float *chunk;                /* decoded PCM staging block */
	int64_t written;
	int64_t pre_roll_sample;     /* drop samples before this after a seek, -1 when idle */
} LJAudioPipeline;

struct LJRenderEngine {
	LJCancelToken *cancel;
	LJProgress progress;
	char error[LJ_ERROR_SIZE];
	LJTimelineView timeline;
	AVFormatContext *input;
	AVFormatContext *output;
	AVBufferRef *vaapi_device;
	AVPacket *demux_packet;
	LJVideoPipeline video;
	LJAudioPipeline audio;
};

#endif
