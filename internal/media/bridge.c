#if defined(__has_include)
#if __has_include("_cgo_export.h")
#include "_cgo_export.h"
#endif
#endif

extern void lecturejetProgress(int percent);

#include "../../native/lecturejet_ffmpeg.c"

static void lj_go_progress(int percent)
{
	lecturejetProgress(percent);
}

int lj_render_file_go(const char *input, const char *output,
	const LJSegment *segments, size_t segment_count,
	const LJRenderOptions *options, char *error, size_t error_size)
{
	return lj_render_file(input, output, segments, segment_count, options,
		lj_go_progress, error, error_size);
}
