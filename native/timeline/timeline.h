/* Source-to-output timestamp mapping over an owned copy of the segment list. */
#ifndef LJ_TIMELINE_H
#define LJ_TIMELINE_H

#include "../common/common.h"

typedef struct LJTimelineView {
	LJSegment *segments;
	size_t count;
	int64_t *output_start;
	int64_t *sample_start;
	int64_t *sample_end;
	int64_t source_end;
	size_t cursor;
} LJTimelineView;

int lj_timeline_init(LJTimelineView *view, const LJSegment *segments, size_t count);
void lj_timeline_free(LJTimelineView *view);
int lj_timeline_lookup_ns(LJTimelineView *view, int64_t source_ns, size_t *index);
int lj_timeline_lookup_sample(LJTimelineView *view, int64_t sample, size_t *index);
int64_t lj_timeline_map_ns(const LJTimelineView *view, size_t index, int64_t source_ns);
int lj_timeline_next_keep(const LJTimelineView *view, size_t from_index, size_t *next_keep_index);

#endif
