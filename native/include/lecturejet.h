/* Public C API: the only header Go (cgo) sees. No FFmpeg types leak through it. */
#ifndef LECTUREJET_H
#define LECTUREJET_H

#include <stddef.h>
#include <stdint.h>

#define LJ_SEGMENT_KEEP 0
#define LJ_SEGMENT_SKIP 1

typedef struct LJCancelToken LJCancelToken;
typedef struct LJAudioDecoder LJAudioDecoder;
typedef struct LJRenderEngine LJRenderEngine;

typedef struct LJProbeInfo {
	int64_t duration_ns;
	int audio_stream;
	int video_stream;
	int video_width;
	int video_height;
	char video_codec[64];
} LJProbeInfo;

typedef struct LJSegment {
	int64_t start_ns;
	int64_t end_ns;
	int action;
} LJSegment;

typedef void (*LJProgressCallback)(int permille);

typedef struct LJRenderOptions {
	const char *video_codec; /* "hevc" or "h264" */
	const char *video_tag;   /* HEVC only: "hev1" (default) or "hvc1" */
	const char *vaapi_device;
	int output_mono;
	int rnnoise_enabled;
	double rnnoise_strength;
	const char *rnnoise_model;
	int threads;
	LJCancelToken *cancel;
	LJProgressCallback progress;
} LJRenderOptions;

LJCancelToken *lj_cancel_token_create(void);
void lj_cancel_token_cancel(LJCancelToken *token);
void lj_cancel_token_destroy(LJCancelToken **token);

int lj_probe_file(const char *path, LJProbeInfo *info, char *error, size_t error_size);
int lj_probe_vaapi(const char *device, const char *codec, int width, int height,
	char *error, size_t error_size);
void lj_set_log_level(int verbose);

LJAudioDecoder *lj_audio_open(const char *path, int output_channels,
	LJCancelToken *cancel, int rnnoise_enabled, double rnnoise_strength,
	const char *rnnoise_model, char *error, size_t error_size);
int lj_audio_read(LJAudioDecoder *decoder, float *output, int max_samples,
	int64_t *start_sample, char *error, size_t error_size);
void lj_audio_close(LJAudioDecoder **decoder);

/* Segments and options are copied; the caller may release them after init returns. */
LJRenderEngine *lj_render_init(const char *input, const char *output,
	const LJSegment *segments, size_t segment_count,
	const LJRenderOptions *options, char *error, size_t error_size);
int lj_render_run(LJRenderEngine *engine, char *error, size_t error_size);
void lj_render_close(LJRenderEngine **engine);

#endif
