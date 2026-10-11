#include "timeline.h"

#include <libavutil/mathematics.h>
#include <stdlib.h>
#include <string.h>

void lj_timeline_free(LJTimelineView *view)
{
	free(view->entries);
	memset(view, 0, sizeof(*view));
}

int lj_timeline_init(LJTimelineView *view, const LJSegment *segments, size_t count)
{
	memset(view, 0, sizeof(*view));
	if (!segments || count == 0)
		return AVERROR(EINVAL);
	view->count = count;
	view->entries = calloc(count, sizeof(*view->entries));
	if (!view->entries)
		return AVERROR(ENOMEM);
	int64_t position = 0;
	int64_t output = 0;
	for (size_t i = 0; i < count; ++i) {
		const LJSegment *segment = &segments[i];
		if (segment->start_ns != position || segment->end_ns <= segment->start_ns ||
			(segment->action != LJ_SEGMENT_KEEP && segment->action != LJ_SEGMENT_SKIP)) {
			lj_timeline_free(view);
			return AVERROR_INVALIDDATA;
		}
		view->entries[i].segment = *segment;
		view->entries[i].output_start_ns = output;
		view->entries[i].sample_start = av_rescale_q_rnd(segment->start_ns, LJ_NS_Q, LJ_SAMPLE_Q, AV_ROUND_UP);
		view->entries[i].sample_end = av_rescale_q_rnd(segment->end_ns, LJ_NS_Q, LJ_SAMPLE_Q, AV_ROUND_UP);
		position = segment->end_ns;
		if (segment->action == LJ_SEGMENT_KEEP)
			output += segment->end_ns - segment->start_ns;
	}
	view->source_end = position;
	return 0;
}

int lj_timeline_lookup_ns(LJTimelineView *view, int64_t source_ns, size_t *index)
{
	if (source_ns < 0 || source_ns >= view->source_end)
		return 0;
	while (view->cursor > 0 && source_ns < view->entries[view->cursor].segment.start_ns)
		--view->cursor;
	while (view->cursor + 1 < view->count && source_ns >= view->entries[view->cursor].segment.end_ns)
		++view->cursor;
	if (source_ns < view->entries[view->cursor].segment.start_ns ||
		source_ns >= view->entries[view->cursor].segment.end_ns)
		return 0;
	*index = view->cursor;
	return 1;
}

int lj_timeline_lookup_sample(LJTimelineView *view, int64_t sample, size_t *index)
{
	if (sample < 0 || sample >= view->entries[view->count - 1].sample_end)
		return 0;
	while (view->cursor > 0 && sample < view->entries[view->cursor].sample_start)
		--view->cursor;
	while (view->cursor + 1 < view->count && sample >= view->entries[view->cursor].sample_end)
		++view->cursor;
	if (sample < view->entries[view->cursor].sample_start || sample >= view->entries[view->cursor].sample_end)
		return 0;
	*index = view->cursor;
	return 1;
}

int64_t lj_timeline_map_ns(const LJTimelineView *view, size_t index, int64_t source_ns)
{
	const LJTimelineEntry *entry = &view->entries[index];
	if (entry->segment.action == LJ_SEGMENT_SKIP)
		return entry->output_start_ns;
	return entry->output_start_ns + source_ns - entry->segment.start_ns;
}

int lj_timeline_next_keep(const LJTimelineView *view, size_t from_index, size_t *next_keep_index)
{
	for (size_t i = from_index + 1; i < view->count; ++i) {
		if (view->entries[i].segment.action == LJ_SEGMENT_KEEP) {
			*next_keep_index = i;
			return 1;
		}
	}
	return 0;
}
