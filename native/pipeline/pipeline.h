/* Entry points shared between the pipeline stages. */
#ifndef LJ_PIPELINE_H
#define LJ_PIPELINE_H

#include "../include/types.h"

/* Silent gaps at least this long are skipped by seeking instead of decoding. */
#define LJ_MIN_SEEK_SILENCE_NS 3000000000LL

static inline int lj_engine_check_cancel(LJRenderEngine *engine)
{
	return lj_abort_if_cancelled(engine->cancel, engine->error, sizeof(engine->error));
}

/* demux.c */
int lj_demux_open(LJRenderEngine *engine, const char *path);
int lj_demux_run(LJRenderEngine *engine);

/* video_pipeline.c */
int lj_video_open_encoder(LJRenderEngine *engine, const LJRenderOptions *options);
int lj_video_open_decoder(LJRenderEngine *engine, int threads);
int lj_video_feed_packet(LJRenderEngine *engine, AVPacket *packet);
/* Decodes every ready frame. draining disables seeking and trailing-silence exit. */
int lj_video_receive_frames(LJRenderEngine *engine, int draining);
int lj_video_send_frame(LJRenderEngine *engine, AVFrame *frame);
/* Sends every filtered frame to the encoder; pts is forced unless AV_NOPTS_VALUE. */
int lj_video_pull_filtered(LJRenderEngine *engine, int64_t pts);

/* audio_pipeline.c */
int lj_audio_pipeline_open_encoder(LJRenderEngine *engine);
int lj_audio_pipeline_open_decoder(LJRenderEngine *engine, const LJRenderOptions *options);
int lj_audio_pipeline_feed_packet(LJRenderEngine *engine, AVPacket *packet);
int lj_audio_pipeline_process(LJRenderEngine *engine);
int lj_audio_pipeline_encode_available(LJRenderEngine *engine, int flush_partial);

/* drain.c */
int lj_pipeline_receive_packets(LJRenderEngine *engine, AVCodecContext *encoder,
	AVStream *stream, AVPacket *packet, int64_t duration_override);
int lj_drain_all(LJRenderEngine *engine);

#endif
