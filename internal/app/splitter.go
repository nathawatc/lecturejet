package app

import (
	"fmt"

	"lecturejet/internal/analyze"
)

// pcmBlockSplitter slices an incoming stream of decoded PCM samples into
// fixed-capacity analyze.PCMBlock blocks, maintaining continuous index and
// sample position cursors.
type pcmBlockSplitter struct {
	blockSize    int
	nextIndex    int
	blockStart   int64
	nextExpected int64
	buffer       []float32
}

func newPCMBlockSplitter(blockSize int) *pcmBlockSplitter {
	if blockSize <= 0 {
		panic("pcmBlockSplitter: blockSize must be positive")
	}
	return &pcmBlockSplitter{
		blockSize:    blockSize,
		nextExpected: -1,
	}
}

// Push consumes incoming PCM samples and emits fixed-size blocks of size blockSize.
func (s *pcmBlockSplitter) Push(chunk []float32, start int64, emit func(analyze.PCMBlock) error) error {
	if len(chunk) == 0 {
		return nil
	}
	if s.nextExpected >= 0 && start != s.nextExpected {
		return fmt.Errorf("unexpected audio decoder discontinuity: expected sample %d, got %d", s.nextExpected, start)
	}
	s.nextExpected = start + int64(len(chunk))

	consumed := 0
	for consumed < len(chunk) {
		if len(s.buffer) == 0 {
			s.blockStart = start + int64(consumed)
			s.buffer = make([]float32, 0, s.blockSize)
		}
		remaining := s.blockSize - len(s.buffer)
		toCopy := len(chunk) - consumed
		if toCopy > remaining {
			toCopy = remaining
		}
		s.buffer = append(s.buffer, chunk[consumed:consumed+toCopy]...)
		consumed += toCopy

		if len(s.buffer) == s.blockSize {
			block := analyze.PCMBlock{
				Index:       s.nextIndex,
				StartSample: s.blockStart,
				Samples:     s.buffer,
			}
			s.nextIndex++
			s.buffer = nil
			if err := emit(block); err != nil {
				return err
			}
		}
	}
	return nil
}

// Flush emits any remaining partial block at EOF.
func (s *pcmBlockSplitter) Flush(emit func(analyze.PCMBlock) error) error {
	if len(s.buffer) == 0 {
		return nil
	}
	block := analyze.PCMBlock{
		Index:       s.nextIndex,
		StartSample: s.blockStart,
		Samples:     s.buffer,
	}
	s.nextIndex++
	s.buffer = nil
	return emit(block)
}

// BufferedSamples returns the number of un-emitted samples currently buffered.
func (s *pcmBlockSplitter) BufferedSamples() int {
	return len(s.buffer)
}
