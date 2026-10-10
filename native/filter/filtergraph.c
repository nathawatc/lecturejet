#include "filtergraph.h"

#include "../hw/vaapi_device.h"

#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libavutil/pixdesc.h>
#include <stdio.h>
#include <string.h>

int lj_filter_is_passthrough(const AVFrame *frame, const LJFilterTarget *target)
{
	return frame->width == target->width && frame->height == target->height &&
		lj_vaapi_frame_sw_format(frame) == target->sw_format;
}

int lj_filter_matches(const LJFilterGraph *filter, const AVFrame *frame)
{
	return filter->graph && filter->in_format == frame->format &&
		filter->in_width == frame->width && filter->in_height == frame->height &&
		filter->in_sw_format == lj_vaapi_frame_sw_format(frame);
}

void lj_filter_free(LJFilterGraph *filter)
{
	avfilter_graph_free(&filter->graph);
	memset(filter, 0, sizeof(*filter));
}

static int lj_filter_alloc(AVFilterGraph *graph, const char *filter_name, const char *instance,
	AVBufferRef *device_ref, AVFilterContext **context)
{
	const AVFilter *filter = avfilter_get_by_name(filter_name);
	if (!filter)
		return AVERROR_FILTER_NOT_FOUND;
	*context = avfilter_graph_alloc_filter(graph, filter, instance);
	if (!*context)
		return AVERROR(ENOMEM);
	if (device_ref) {
		(*context)->hw_device_ctx = av_buffer_ref(device_ref);
		if (!(*context)->hw_device_ctx)
			return AVERROR(ENOMEM);
	}
	return 0;
}

static int lj_filter_create(AVFilterGraph *graph, const char *filter_name, const char *instance,
	AVBufferRef *device_ref, const char *args, AVFilterContext **context)
{
	int ret = lj_filter_alloc(graph, filter_name, instance, device_ref, context);
	return ret < 0 ? ret : avfilter_init_str(*context, args);
}

static int lj_filter_create_source(AVFilterGraph *graph, const AVFrame *frame,
	AVRational time_base, AVFilterContext **context)
{
	int ret = lj_filter_alloc(graph, "buffer", "in", NULL, context);
	if (ret < 0)
		return ret;
	AVBufferSrcParameters *params = av_buffersrc_parameters_alloc();
	if (!params)
		return AVERROR(ENOMEM);
	params->format = frame->format;
	params->width = frame->width;
	params->height = frame->height;
	params->time_base = time_base;
	params->sample_aspect_ratio = frame->sample_aspect_ratio.num > 0 && frame->sample_aspect_ratio.den > 0
		? frame->sample_aspect_ratio : (AVRational){1, 1};
	params->color_space = frame->colorspace;
	params->color_range = frame->color_range;
	if (frame->format == AV_PIX_FMT_VAAPI && frame->hw_frames_ctx)
		params->hw_frames_ctx = frame->hw_frames_ctx;
	ret = av_buffersrc_parameters_set(*context, params);
	av_free(params);
	return ret < 0 ? ret : avfilter_init_str(*context, NULL);
}

int lj_filter_configure(LJFilterGraph *filter, const AVFrame *frame, AVBufferRef *device_ref,
	const LJFilterTarget *target, char *error, size_t error_size)
{
	lj_filter_free(filter);
	AVFilterGraph *graph = avfilter_graph_alloc();
	if (!graph)
		return AVERROR(ENOMEM);

	AVFilterContext *src = NULL;
	AVFilterContext *upload = NULL;
	AVFilterContext *scale = NULL;
	AVFilterContext *sink = NULL;
	char scale_args[128];
	snprintf(scale_args, sizeof(scale_args), "w=%d:h=%d:format=%s", target->width, target->height,
		av_get_pix_fmt_name(target->sw_format));

	int ret = lj_filter_create_source(graph, frame, target->time_base, &src);
	if (ret >= 0 && frame->format != AV_PIX_FMT_VAAPI)
		ret = lj_filter_create(graph, "hwupload", "upload", device_ref, NULL, &upload);
	if (ret >= 0)
		ret = lj_filter_create(graph, "scale_vaapi", "scale", device_ref, scale_args, &scale);
	if (ret >= 0)
		ret = lj_filter_create(graph, "buffersink", "out", NULL, "pixel_formats=vaapi", &sink);
	if (ret >= 0 && upload) {
		ret = avfilter_link(src, 0, upload, 0);
		if (ret >= 0)
			ret = avfilter_link(upload, 0, scale, 0);
	} else if (ret >= 0) {
		ret = avfilter_link(src, 0, scale, 0);
	}
	if (ret >= 0)
		ret = avfilter_link(scale, 0, sink, 0);
	if (ret >= 0) {
		ret = avfilter_graph_config(graph, NULL);
		if (ret < 0)
			lj_fferror(error, error_size, "configuring video filter graph", ret);
	}
	if (ret < 0) {
		avfilter_graph_free(&graph);
		return ret;
	}

	filter->graph = graph;
	filter->src = src;
	filter->sink = sink;
	filter->in_format = frame->format;
	filter->in_width = frame->width;
	filter->in_height = frame->height;
	filter->in_sw_format = lj_vaapi_frame_sw_format(frame);
	return 0;
}
