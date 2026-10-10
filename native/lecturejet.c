/*
 * cgo compiles only the .c files inside the Go package directory, so
 * internal/media/bridge.c includes this single translation unit. Every file
 * below also compiles on its own: `make native-check`.
 */
#include "common/errors.c"
#include "common/cancel.c"
#include "common/progress.c"
#include "common/samples.c"
#include "hw/vaapi_device.c"
#include "config/encoder_profile.c"
#include "filter/filtergraph.c"
#include "timeline/timeline.c"
#include "audio/resample.c"
#include "audio/rnnoise.c"
#include "audio/decoder.c"
#include "probe/probe.c"
#include "pipeline/demux.c"
#include "pipeline/video_pipeline.c"
#include "pipeline/audio_pipeline.c"
#include "pipeline/drain.c"
#include "pipeline/engine.c"
