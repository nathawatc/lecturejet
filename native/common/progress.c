#include "common.h"

#include <libavutil/common.h>
#include <libavutil/mathematics.h>

void lj_progress_init(LJProgress *progress, LJProgressCallback callback)
{
	progress->callback = callback;
	progress->last_permille = -1;
}

void lj_progress_report(LJProgress *progress, int permille)
{
	permille = av_clip(permille, 0, 1000);
	if (!progress->callback || permille <= progress->last_permille)
		return;
	progress->callback(permille);
	progress->last_permille = permille;
}

void lj_progress_update(LJProgress *progress, int64_t position, int64_t total)
{
	if (total > 0)
		lj_progress_report(progress, (int)av_rescale(position, 1000, total));
}
