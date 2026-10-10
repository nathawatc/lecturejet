/*
 * Single source of truth for every encoder parameter. Edit the defaults in
 * encoder_profile.c; render and VAAPI probe both go through these functions.
 */
#ifndef LJ_ENCODER_PROFILE_H
#define LJ_ENCODER_PROFILE_H

#include "../common/common.h"

#include <libavcodec/avcodec.h>
#include <libavutil/buffer.h>
#include <libavutil/pixfmt.h>

typedef struct LJVideoConfig {
	const char *encoder_name;
	enum AVCodecID codec_id;
	enum AVPixelFormat sw_pix_fmt; /* surface format fed to the encoder */
	uint32_t codec_tag;            /* container FourCC; 0 keeps the encoder default */
	AVRational time_base;
	const char *rc_mode;           /* VAAPI rate control mode name */
	int qp;
	int gop_duration_sec;          /* gop_size = rounded fps * gop_duration_sec */
	int refs;
	int max_b_frames;
	int async_depth;
	int compression_level;
	int disable_sei;
	int hw_pool_size;
} LJVideoConfig;

typedef struct LJAudioConfig {
	enum AVCodecID codec_id;
	enum AVSampleFormat sample_fmt;
	int sample_rate;
	int bitrate_mono;
	int bitrate_stereo;
	int default_frame_size;
} LJAudioConfig;

/* codec: "hevc" or "h264". hevc_tag: "hev1" (default) or "hvc1", used for HEVC only. */
int lj_video_profile_select(const char *codec, const char *hevc_tag, LJVideoConfig *config,
	char *error, size_t error_size);
const LJAudioConfig *lj_audio_profile(void);

/* Creates and opens a VAAPI encoder bound to device_ref. */
int lj_video_encoder_open(const LJVideoConfig *config, AVBufferRef *device_ref,
	int width, int height, AVRational fps, int global_header,
	AVCodecContext **encoder, char *error, size_t error_size);
int lj_audio_encoder_open(const LJAudioConfig *config, int channels, int global_header,
	AVCodecContext **encoder);

#endif
