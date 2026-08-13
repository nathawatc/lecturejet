package audio

import "lecturejet/internal/timeline"

type SampleRange struct {
	Start  int64
	End    int64
	Action timeline.Action
}

type AudioClock struct {
	SamplesWritten int64
	SampleRate     int
}

func SampleRanges(value timeline.Timeline, sampleRate int) []SampleRange {
	segments := value.Segments()
	ranges := make([]SampleRange, 0, len(segments))
	for _, segment := range segments {
		start := timeline.SampleIndexAtOrAfter(segment.Start, sampleRate)
		end := timeline.SampleIndexAtOrAfter(segment.End, sampleRate)
		if end > start {
			ranges = append(ranges, SampleRange{Start: start, End: end, Action: segment.Action})
		}
	}
	return ranges
}

func SplitChunk(ranges []SampleRange, start int64, count int) []SampleRange {
	if count <= 0 {
		return nil
	}
	end := start + int64(count)
	pieces := make([]SampleRange, 0, len(ranges))
	for _, value := range ranges {
		pieceStart := value.Start
		if pieceStart < start {
			pieceStart = start
		}
		pieceEnd := value.End
		if pieceEnd > end {
			pieceEnd = end
		}
		if pieceEnd > pieceStart {
			pieces = append(pieces, SampleRange{Start: pieceStart, End: pieceEnd, Action: value.Action})
		}
	}
	return pieces
}

func (c *AudioClock) Write(samples int64) {
	if samples > 0 {
		c.SamplesWritten += samples
	}
}

func (c AudioClock) PTS() timeline.TimeNS {
	return timeline.TimeForSamples(c.SamplesWritten, c.SampleRate)
}
