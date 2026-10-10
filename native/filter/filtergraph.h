/* Hardware conversion graph: [hwupload] -> scale_vaapi -> VAAPI sink. */
#ifndef LJ_FILTERGRAPH_H
#define LJ_FILTERGRAPH_H

#include "../common/common.h"

#include <libavfilter/avfilter.h>
#include <libavutil/buffer.h>
#include <libavutil/frame.h>
#include <libavutil/pixfmt.h>

typedef struct LJFilterTarget {
	int width;
	int height;
	enum AVPixelFormat sw_format;
	AVRational time_base;
} LJFilterTarget;

typedef struct LJFilterGraph {
	AVFilterGraph *graph;
	AVFilterContext *src;
	AVFilterContext *sink;
	int in_format;
	int in_width;
	int in_height;
	enum AVPixelFormat in_sw_format;
} LJFilterGraph;

/* True when frame can go straight to the encoder (VAAPI surface already in target shape). */
int lj_filter_is_passthrough(const AVFrame *frame, const LJFilterTarget *target);
/* True when the configured graph accepts frame's format and size unchanged. */
int lj_filter_matches(const LJFilterGraph *filter, const AVFrame *frame);
int lj_filter_configure(LJFilterGraph *filter, const AVFrame *frame, AVBufferRef *device_ref,
	const LJFilterTarget *target, char *error, size_t error_size);
void lj_filter_free(LJFilterGraph *filter);

#endif
