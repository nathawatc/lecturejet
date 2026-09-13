package timeline

import (
	"fmt"
	"math"
	"sort"
)

type TimeNS int64

const (
	Keep Action = iota
	Cut
)

type Action uint8

type Segment struct {
	Start  TimeNS
	End    TimeNS
	Action Action
}

type Timeline struct {
	end      TimeNS
	segments []Segment
	keptEnds []TimeNS
}

func New(sourceEnd TimeNS, segments []Segment) (Timeline, error) {
	if sourceEnd <= 0 {
		return Timeline{}, fmt.Errorf("source duration must be positive")
	}
	if len(segments) == 0 {
		return Timeline{}, fmt.Errorf("timeline cannot be empty")
	}
	result := make([]Segment, 0, len(segments))
	position := TimeNS(0)
	for i, segment := range segments {
		if segment.Action != Keep && segment.Action != Cut {
			return Timeline{}, fmt.Errorf("segment %d has invalid action %d", i, segment.Action)
		}
		if segment.Start != position {
			return Timeline{}, fmt.Errorf("segment %d starts at %d, expected %d", i, segment.Start, position)
		}
		if segment.End <= segment.Start {
			return Timeline{}, fmt.Errorf("segment %d has non-positive duration", i)
		}
		if segment.End > sourceEnd {
			return Timeline{}, fmt.Errorf("segment %d ends after source duration", i)
		}
		if n := len(result); n > 0 && result[n-1].Action == segment.Action {
			result[n-1].End = segment.End
		} else {
			result = append(result, segment)
		}
		position = segment.End
	}
	if position != sourceEnd {
		return Timeline{}, fmt.Errorf("timeline ends at %d, expected %d", position, sourceEnd)
	}
	keptEnds := make([]TimeNS, len(result))
	kept := TimeNS(0)
	for i, segment := range result {
		if segment.Action == Keep {
			kept += segment.End - segment.Start
		}
		keptEnds[i] = kept
	}
	return Timeline{end: sourceEnd, segments: result, keptEnds: keptEnds}, nil
}

func FromCuts(sourceEnd TimeNS, cuts []Segment) (Timeline, error) {
	if sourceEnd <= 0 {
		return Timeline{}, fmt.Errorf("source duration must be positive")
	}
	cutRanges := make([]Segment, 0, len(cuts))
	for i, cut := range cuts {
		if cut.Action != Cut {
			return Timeline{}, fmt.Errorf("cut %d action is not Cut", i)
		}
		start := maxTime(0, minTime(sourceEnd, cut.Start))
		end := maxTime(0, minTime(sourceEnd, cut.End))
		if end <= start {
			continue
		}
		cutRanges = append(cutRanges, Segment{Start: start, End: end, Action: Cut})
	}
	sort.Slice(cutRanges, func(i, j int) bool {
		if cutRanges[i].Start == cutRanges[j].Start {
			return cutRanges[i].End < cutRanges[j].End
		}
		return cutRanges[i].Start < cutRanges[j].Start
	})
	merged := make([]Segment, 0, len(cutRanges))
	for _, cut := range cutRanges {
		if n := len(merged); n > 0 && cut.Start <= merged[n-1].End {
			if cut.End > merged[n-1].End {
				merged[n-1].End = cut.End
			}
			continue
		}
		merged = append(merged, cut)
	}
	segments := make([]Segment, 0, len(merged)*2+1)
	position := TimeNS(0)
	for _, cut := range merged {
		if cut.Start > position {
			segments = append(segments, Segment{Start: position, End: cut.Start, Action: Keep})
		}
		segments = append(segments, cut)
		position = cut.End
	}
	if position < sourceEnd {
		segments = append(segments, Segment{Start: position, End: sourceEnd, Action: Keep})
	}
	if len(segments) == 0 {
		segments = []Segment{{Start: 0, End: sourceEnd, Action: Keep}}
	}
	return New(sourceEnd, segments)
}

func (t Timeline) SourceDuration() TimeNS { return t.end }

func (t Timeline) OutputDuration() TimeNS {
	if len(t.keptEnds) == 0 {
		return 0
	}
	return t.keptEnds[len(t.keptEnds)-1]
}

func (t Timeline) Segments() []Segment {
	return append([]Segment(nil), t.segments...)
}

func (t Timeline) Cuts() int {
	count := 0
	for _, segment := range t.segments {
		if segment.Action == Cut {
			count++
		}
	}
	return count
}

func (t Timeline) Lookup(source TimeNS) (Segment, int, bool) {
	if len(t.segments) == 0 || source < 0 || source >= t.end {
		return Segment{}, -1, false
	}
	i := sort.Search(len(t.segments), func(i int) bool { return t.segments[i].End > source })
	return t.segments[i], i, true
}

func (t Timeline) MapSourceToOutput(source TimeNS) TimeNS {
	if source <= 0 {
		return 0
	}
	if source >= t.end {
		return t.OutputDuration()
	}
	segment, index, ok := t.Lookup(source)
	if !ok {
		return t.OutputDuration()
	}
	outputStart := t.keptEnds[index] - keptDuration(segment)
	if segment.Action == Cut {
		return outputStart
	}
	return outputStart + source - segment.Start
}

func keptDuration(segment Segment) TimeNS {
	if segment.Action == Keep {
		return segment.End - segment.Start
	}
	return 0
}

func SampleIndexAtOrAfter(ns TimeNS, sampleRate int) int64 {
	if ns <= 0 {
		return 0
	}
	if sampleRate <= 0 {
		return 0
	}
	if int64(ns) > math.MaxInt64/int64(sampleRate) {
		return math.MaxInt64
	}
	value := int64(ns) * int64(sampleRate)
	result := value / 1_000_000_000
	if value%1_000_000_000 != 0 && result < math.MaxInt64 {
		result++
	}
	return result
}

func TimeForSamples(samples int64, sampleRate int) TimeNS {
	if samples <= 0 || sampleRate <= 0 {
		return 0
	}
	if samples > math.MaxInt64/1_000_000_000 {
		return math.MaxInt64
	}
	return TimeNS(samples * 1_000_000_000 / int64(sampleRate))
}

func maxTime(a, b TimeNS) TimeNS {
	if a > b {
		return a
	}
	return b
}

func minTime(a, b TimeNS) TimeNS {
	if a < b {
		return a
	}
	return b
}
