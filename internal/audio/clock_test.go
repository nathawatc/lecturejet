package audio

import (
	"testing"

	"lecturejet/internal/timeline"
)

func TestSampleRangesAndChunkBoundaries(t *testing.T) {
	value, err := timeline.New(1_000_000_000, []timeline.Segment{
		{Start: 0, End: 333_333_334, Action: timeline.Keep},
		{Start: 333_333_334, End: 666_666_667, Action: timeline.Cut},
		{Start: 666_666_667, End: 1_000_000_000, Action: timeline.Keep},
	})
	if err != nil {
		t.Fatal(err)
	}
	ranges := SampleRanges(value, 48_000)
	if len(ranges) != 3 || ranges[0].End != 16_001 || ranges[1].Start != 16_001 || ranges[2].Start != 32_001 {
		t.Fatalf("ranges=%v", ranges)
	}
	pieces := SplitChunk(ranges, 15_500, 17_000)
	if len(pieces) != 3 || pieces[0].Action != timeline.Keep || pieces[0].End != 16_001 ||
		pieces[1].Action != timeline.Cut || pieces[1].Start != 16_001 ||
		pieces[2].Action != timeline.Keep || pieces[2].Start != 32_001 {
		t.Fatalf("cross-boundary pieces=%v", pieces)
	}
}

func TestFirstLastAndMultipleCuts(t *testing.T) {
	value, err := timeline.FromCuts(1_000_000_000, []timeline.Segment{
		{Start: 0, End: 100_000_001, Action: timeline.Cut},
		{Start: 400_000_001, End: 500_000_001, Action: timeline.Cut},
		{Start: 900_000_001, End: 1_000_000_000, Action: timeline.Cut},
	})
	if err != nil {
		t.Fatal(err)
	}
	ranges := SampleRanges(value, 48_000)
	for _, value := range ranges {
		if value.Start < 0 || value.End <= value.Start {
			t.Fatalf("invalid sample range=%v", value)
		}
	}
	if ranges[0].Action != timeline.Cut || ranges[len(ranges)-1].Action != timeline.Cut {
		t.Fatalf("edge cuts lost: %v", ranges)
	}
}

func TestAudioClockPTSUsesSamplesAndStaysMonotonic(t *testing.T) {
	clock := AudioClock{SampleRate: 48_000}
	previous := timeline.TimeNS(0)
	for _, samples := range []int64{0, 1, 1023, 1024, 48_000, 96_000} {
		clock.SamplesWritten = samples
		if clock.PTS() < previous {
			t.Fatalf("PTS decreased at samples=%d", samples)
		}
		previous = clock.PTS()
	}
	clock.SamplesWritten = 0
	clock.Write(1024)
	if clock.PTS() != timeline.TimeNS(21_333_333) {
		t.Fatalf("PTS=%d", clock.PTS())
	}
}
