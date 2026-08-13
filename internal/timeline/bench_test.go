package timeline

import "testing"

var benchmarkSinkTime TimeNS
var benchmarkSinkIndex int64

func benchmarkTimeline(b *testing.B) Timeline {
	segments := make([]Segment, 0, 20_001)
	position := TimeNS(0)
	for i := 0; i < 20_000; i++ {
		action := Keep
		if i%2 == 1 {
			action = Cut
		}
		segments = append(segments, Segment{Start: position, End: position + 1_000_000, Action: action})
		position += 1_000_000
	}
	segments = append(segments, Segment{Start: position, End: position + 1_000_000, Action: Keep})
	value, err := New(position+1_000_000, segments)
	if err != nil {
		b.Fatal(err)
	}
	return value
}

func BenchmarkTimelineLookup(b *testing.B) {
	value := benchmarkTimeline(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, index, ok := value.Lookup(TimeNS((i % 20_001) * 1_000_000))
		if ok {
			benchmarkSinkIndex += int64(index)
		}
	}
}

func BenchmarkTimelineSourceOutputMapping(b *testing.B) {
	value := benchmarkTimeline(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkSinkTime = value.MapSourceToOutput(TimeNS((i % 20_001) * 1_000_000))
	}
}

func BenchmarkSampleBoundaryMapping(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkSinkIndex = SampleIndexAtOrAfter(TimeNS(i%1_000_000_000), 48_000)
	}
}
