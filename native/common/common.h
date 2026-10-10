/* Cross-cutting helpers: errors, cancellation, progress, sample buffers. */
#ifndef LJ_COMMON_H
#define LJ_COMMON_H

#include "../include/lecturejet.h"

#include <libavformat/avformat.h>
#include <libavutil/rational.h>

/* Analysis and encode sample rate. Go mirrors it in config.SampleRate. */
#define LJ_SAMPLE_RATE 48000
#define LJ_SAMPLE_Q ((AVRational){1, LJ_SAMPLE_RATE})
#define LJ_NS_Q ((AVRational){1, 1000000000})
#define LJ_ERROR_SIZE 512

struct LJCancelToken {
	int cancelled;
};

/* errors.c */
void lj_error(char *error, size_t error_size, const char *format, ...);
void lj_fferror(char *error, size_t error_size, const char *operation, int code);
/* Cancellation wins, then a prepared detail message, then the FFmpeg code. */
void lj_report_failure(char *error, size_t error_size, const char *detail,
	const LJCancelToken *cancel, const char *operation, int code);

/* cancel.c */
int lj_cancel_requested(const LJCancelToken *token);
int lj_abort_if_cancelled(const LJCancelToken *token, char *error, size_t error_size);
void lj_set_interrupt_callback(AVFormatContext *format, LJCancelToken *token);

/* progress.c: monotonic permille reporting. */
typedef struct LJProgress {
	LJProgressCallback callback;
	int last_permille;
} LJProgress;

void lj_progress_init(LJProgress *progress, LJProgressCallback callback);
void lj_progress_report(LJProgress *progress, int permille);
void lj_progress_update(LJProgress *progress, int64_t position, int64_t total);

/* samples.c: growable interleaved float FIFO. */
typedef struct LJSampleBuffer {
	float *data;
	size_t frames;
	size_t capacity;
	int channels;
} LJSampleBuffer;

/* Appends room for frames (> 0) and returns the new region, or NULL on overflow/OOM. */
float *lj_samples_extend(LJSampleBuffer *buffer, size_t frames);
void lj_samples_discard(LJSampleBuffer *buffer, size_t frames);
void lj_samples_free(LJSampleBuffer *buffer);

#endif
