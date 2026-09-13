#ifndef LECTUREJET_FFMPEG_H
#define LECTUREJET_FFMPEG_H

#include <stddef.h>
#include <stdint.h>

typedef struct LJProbeInfo {
    int64_t duration_ns;
    int audio_stream;
    int video_stream;
    int video_width;
    int video_height;
    char video_codec[64];
} LJProbeInfo;

typedef struct LJAudioDecoder LJAudioDecoder;
typedef struct LJCancelToken LJCancelToken;

typedef struct LJSegment {
    int64_t start_ns;
    int64_t end_ns;
    int action;
} LJSegment;

typedef struct LJRenderOptions {
    const char *video_codec;
    const char *video_tag;
    const char *vaapi_device;
    int output_mono;
    int rnnoise_enabled;
    double rnnoise_strength;
    const char *rnnoise_model;
    LJCancelToken *cancel;
} LJRenderOptions;

typedef void (*LJProgressCallback)(int percent);

LJCancelToken *lj_cancel_token_create(void);
void lj_cancel_token_cancel(LJCancelToken *token);
void lj_cancel_token_destroy(LJCancelToken **token);

int lj_probe_file(const char *path, LJProbeInfo *info, char *error, size_t error_size);
int lj_probe_vaapi(const char *device, const char *codec, int width, int height,
                   char *error, size_t error_size);

LJAudioDecoder *lj_audio_open(const char *path, int output_channels,
                              LJCancelToken *cancel,
                              int rnnoise_enabled, double rnnoise_strength,
                              const char *rnnoise_model, char *error,
                              size_t error_size);
int lj_audio_read(LJAudioDecoder *decoder, float *output, int max_samples,
                  int64_t *start_sample, char *error, size_t error_size);
void lj_audio_close(LJAudioDecoder **decoder);

int lj_render_file(const char *input, const char *output,
                   const LJSegment *segments, size_t segment_count,
                   const LJRenderOptions *options, LJProgressCallback progress,
                   char *error, size_t error_size);

int lj_remux_hevc_hvc1(const char *input, const char *output,
                       LJCancelToken *cancel, char *error, size_t error_size);

#endif
