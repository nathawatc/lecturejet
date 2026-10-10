#include "internal.h"

void lj_timeline_free(LJTimelineView *view)
{
	if (!view)
		return;
	free(view->output_start);
	free(view->sample_start);
	free(view->sample_end);
	memset(view, 0, sizeof(*view));
}

int lj_timeline_init(LJTimelineView *view, const LJSegment *segments, size_t count)
{
	if (!view || !segments || count == 0)
		return AVERROR(EINVAL);
	memset(view, 0, sizeof(*view));
	view->segments = segments;
	view->count = count;
	view->output_start = calloc(count, sizeof(*view->output_start));
	view->sample_start = calloc(count, sizeof(*view->sample_start));
	view->sample_end = calloc(count, sizeof(*view->sample_end));
	if (!view->output_start || !view->sample_start || !view->sample_end) {
		lj_timeline_free(view);
		return AVERROR(ENOMEM);
	}
	int64_t position = 0;
	int64_t output = 0;
	for (size_t i = 0; i < count; ++i) {
		const LJSegment *segment = &segments[i];
		if (segment->start_ns != position || segment->end_ns <= segment->start_ns ||
			(segment->action != 0 && segment->action != 1)) {
			lj_timeline_free(view);
			return AVERROR_INVALIDDATA;
		}
		view->output_start[i] = output;
		view->sample_start[i] = av_rescale_q_rnd(segment->start_ns, (AVRational){1, 1000000000},
			(AVRational){1, 48000}, AV_ROUND_UP);
		view->sample_end[i] = av_rescale_q_rnd(segment->end_ns, (AVRational){1, 1000000000},
			(AVRational){1, 48000}, AV_ROUND_UP);
		position = segment->end_ns;
		if (segment->action == 0)
			output += segment->end_ns - segment->start_ns;
	}
	view->source_end = position;
	return 0;
}

int lj_timeline_lookup_ns(LJTimelineView *view, int64_t source_ns, size_t *index)
{
	if (!view || source_ns < 0 || source_ns >= view->source_end)
		return 0;
	while (view->cursor > 0 && source_ns < view->segments[view->cursor].start_ns)
		--view->cursor;
	while (view->cursor + 1 < view->count && source_ns >= view->segments[view->cursor].end_ns)
		++view->cursor;
	if (source_ns < view->segments[view->cursor].start_ns ||
		source_ns >= view->segments[view->cursor].end_ns)
		return 0;
	if (index)
		*index = view->cursor;
	return 1;
}

int lj_timeline_lookup_sample(LJTimelineView *view, int64_t sample, size_t *index)
{
	if (!view || sample < 0 || sample >= view->sample_end[view->count - 1])
		return 0;
	while (view->cursor > 0 && sample < view->sample_start[view->cursor])
		--view->cursor;
	while (view->cursor + 1 < view->count && sample >= view->sample_end[view->cursor])
		++view->cursor;
	if (sample < view->sample_start[view->cursor] || sample >= view->sample_end[view->cursor])
		return 0;
	if (index)
		*index = view->cursor;
	return 1;
}

int64_t lj_timeline_map_ns(const LJTimelineView *view, size_t index, int64_t source_ns)
{
	const LJSegment *segment = &view->segments[index];
	if (segment->action == 1)
		return view->output_start[index];
	return view->output_start[index] + source_ns - segment->start_ns;
}

int lj_timeline_next_keep(const LJTimelineView *view, size_t from_index, size_t *next_keep_index)
{
	if (!view)
		return 0;
	for (size_t i = from_index + 1; i < view->count; ++i) {
		if (view->segments[i].action == 0) {
			if (next_keep_index)
				*next_keep_index = i;
			return 1;
		}
	}
	return 0;
}
