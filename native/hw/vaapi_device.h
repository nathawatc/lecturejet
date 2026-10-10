/* VAAPI device lifecycle, hardware frame pools, and frame inspection. */
#ifndef LJ_VAAPI_DEVICE_H
#define LJ_VAAPI_DEVICE_H

#include "../common/common.h"

#include <libavcodec/avcodec.h>
#include <libavutil/buffer.h>
#include <libavutil/frame.h>
#include <libavutil/pixfmt.h>

/* device may be NULL to let libva pick a node. */
int lj_vaapi_device_open(const char *device, AVBufferRef **device_ref,
	char *error, size_t error_size);
int lj_vaapi_frames_alloc(AVBufferRef *device_ref, enum AVPixelFormat sw_format,
	int width, int height, int pool_size, AVBufferRef **frames_ref);
/* Software format backing a VAAPI frame, or AV_PIX_FMT_NONE for any other frame. */
enum AVPixelFormat lj_vaapi_frame_sw_format(const AVFrame *frame);
/* AVCodecContext.get_format callback: prefer VAAPI surfaces, else the first offered format. */
enum AVPixelFormat lj_vaapi_get_format(AVCodecContext *context, const enum AVPixelFormat *formats);

#endif
