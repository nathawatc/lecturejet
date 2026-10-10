package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"codeberg.org/nathawat/lecturejet/internal/analyze"
)

func verifyBlocks(t *testing.T, blocks []analyze.PCMBlock, original []float32, blockSize int, expectedStart int64) {
	t.Helper()
	var concatenated []float32
	for i, b := range blocks {
		if b.Index != i {
			t.Fatalf("block %d: Index=%d, want %d", i, b.Index, i)
		}
		expectedBlockStart := expectedStart + int64(i*blockSize)
		if b.StartSample != expectedBlockStart {
			t.Fatalf("block %d: StartSample=%d, want %d", i, b.StartSample, expectedBlockStart)
		}
		if i < len(blocks)-1 {
			if len(b.Samples) != blockSize {
				t.Fatalf("block %d: len=%d, want %d", i, len(b.Samples), blockSize)
			}
		} else {
			if len(b.Samples) == 0 || len(b.Samples) > blockSize {
				t.Fatalf("final block %d: invalid len=%d (blockSize=%d)", i, len(b.Samples), blockSize)
			}
		}
		concatenated = append(concatenated, b.Samples...)
	}
	if len(concatenated) != len(original) {
		t.Fatalf("total samples=%d, want %d", len(concatenated), len(original))
	}
	for i := range original {
		if concatenated[i] != original[i] {
			t.Fatalf("sample %d mismatch: got %v, want %v", i, concatenated[i], original[i])
		}
	}
}

// 1. input chunks that divide 96_000 exactly
func TestSplitterExactDivisorChunks(t *testing.T) {
	const blockSize = 96_000
	const chunkSize = 9_600 // 10 chunks per block
	const totalSamples = blockSize * 3

	original := make([]float32, totalSamples)
	for i := range original {
		original[i] = float32(i) * 0.0001
	}

	splitter := newPCMBlockSplitter(blockSize)
	var blocks []analyze.PCMBlock
	emit := func(b analyze.PCMBlock) error {
		blocks = append(blocks, b)
		return nil
	}

	for offset := 0; offset < totalSamples; offset += chunkSize {
		chunk := original[offset : offset+chunkSize]
		if err := splitter.Push(chunk, int64(offset), emit); err != nil {
			t.Fatalf("Push error: %v", err)
		}
	}
	if err := splitter.Flush(emit); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	if len(blocks) != 3 {
		t.Fatalf("emitted %d blocks, want 3", len(blocks))
	}
	verifyBlocks(t, blocks, original, blockSize, 0)
}

// 2. input chunks that cross the boundary, especially 8192-sized chunks
func TestSplitterChunksCrossingBoundary8192(t *testing.T) {
	const blockSize = 96_000
	const chunkSize = 8_192
	const totalSamples = 250_000 // 2 full blocks (96k each) + 1 partial block (58k)

	original := make([]float32, totalSamples)
	for i := range original {
		original[i] = float32(i%1000) * 0.001
	}

	splitter := newPCMBlockSplitter(blockSize)
	var blocks []analyze.PCMBlock
	emit := func(b analyze.PCMBlock) error {
		blocks = append(blocks, b)
		return nil
	}

	for offset := 0; offset < totalSamples; {
		end := min(offset+chunkSize, totalSamples)
		chunk := original[offset:end]
		if err := splitter.Push(chunk, int64(offset), emit); err != nil {
			t.Fatalf("Push error: %v", err)
		}
		offset = end
	}
	if err := splitter.Flush(emit); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	if len(blocks) != 3 {
		t.Fatalf("emitted %d blocks, want 3", len(blocks))
	}
	if len(blocks[0].Samples) != blockSize {
		t.Fatalf("block 0 len=%d, want %d", len(blocks[0].Samples), blockSize)
	}
	if len(blocks[1].Samples) != blockSize {
		t.Fatalf("block 1 len=%d, want %d", len(blocks[1].Samples), blockSize)
	}
	if len(blocks[2].Samples) != 58_000 {
		t.Fatalf("block 2 len=%d, want 58000", len(blocks[2].Samples))
	}
	verifyBlocks(t, blocks, original, blockSize, 0)
}

// 3. one decoder read large enough to produce multiple analysis blocks
func TestSplitterLargeDecoderReadMultipleBlocks(t *testing.T) {
	const blockSize = 96_000
	const totalSamples = 250_000

	original := make([]float32, totalSamples)
	for i := range original {
		original[i] = float32(i%777) * 0.001
	}

	splitter := newPCMBlockSplitter(blockSize)
	var blocks []analyze.PCMBlock
	emit := func(b analyze.PCMBlock) error {
		blocks = append(blocks, b)
		return nil
	}

	// Entire 250,000 samples passed in a single read
	if err := splitter.Push(original, 0, emit); err != nil {
		t.Fatalf("Push error: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("after single large Push, emitted %d blocks, want 2 full blocks", len(blocks))
	}
	if err := splitter.Flush(emit); err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("after Flush, emitted %d blocks, want 3", len(blocks))
	}

	verifyBlocks(t, blocks, original, blockSize, 0)
}

// 4. final partial block (and empty stream)
func TestSplitterFinalPartialBlock(t *testing.T) {
	const blockSize = 96_000

	t.Run("shorter than single block", func(t *testing.T) {
		const totalSamples = 10_000
		original := make([]float32, totalSamples)
		for i := range original {
			original[i] = float32(i)
		}

		splitter := newPCMBlockSplitter(blockSize)
		var blocks []analyze.PCMBlock
		emit := func(b analyze.PCMBlock) error {
			blocks = append(blocks, b)
			return nil
		}

		// Push 8192 then remaining 1808
		if err := splitter.Push(original[:8192], 0, emit); err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 0 {
			t.Fatalf("emitted %d blocks before flush, want 0", len(blocks))
		}
		if err := splitter.Push(original[8192:], 8192, emit); err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 0 {
			t.Fatalf("emitted %d blocks before flush, want 0", len(blocks))
		}
		if err := splitter.Flush(emit); err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 1 {
			t.Fatalf("emitted %d blocks after flush, want 1", len(blocks))
		}
		verifyBlocks(t, blocks, original, blockSize, 0)
	})

	t.Run("empty stream", func(t *testing.T) {
		splitter := newPCMBlockSplitter(blockSize)
		var blocks []analyze.PCMBlock
		emit := func(b analyze.PCMBlock) error {
			blocks = append(blocks, b)
			return nil
		}
		if err := splitter.Flush(emit); err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 0 {
			t.Fatalf("emitted %d blocks from empty stream, want 0", len(blocks))
		}
	})
}

// 5, 6, 7, 8. Non-zero start position, exact Index sequence, exact StartSample sequence
func TestSplitterNonZeroStartAndSequence(t *testing.T) {
	const blockSize = 96_000
	const initialStart int64 = 48_000
	const totalSamples = 200_000

	original := make([]float32, totalSamples)
	for i := range original {
		original[i] = float32(i) * 0.1
	}

	splitter := newPCMBlockSplitter(blockSize)
	var blocks []analyze.PCMBlock
	emit := func(b analyze.PCMBlock) error {
		blocks = append(blocks, b)
		return nil
	}

	chunkSize := 8192
	for offset := 0; offset < totalSamples; {
		end := min(offset+chunkSize, totalSamples)
		chunk := original[offset:end]
		if err := splitter.Push(chunk, initialStart+int64(offset), emit); err != nil {
			t.Fatalf("Push error: %v", err)
		}
		offset = end
	}
	if err := splitter.Flush(emit); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	verifyBlocks(t, blocks, original, blockSize, initialStart)
}

// Regression case demonstrating old failure:
// 8192-sample reads crossing the 96000-sample boundary.
// Under the old implementation:
//
//	buffer = append(buffer, readBuffer[:count]...)
//	if len(buffer) == blockSamples { emit... }
//
// After 11 reads of 8192: len(buffer) = 90,112.
// Read 12 adds 8192: len(buffer) = 98,304.
// len(buffer) == blockSamples (96000) is never true, so 0 blocks were emitted
// during Push, and at EOF a single oversized block of 106,496 samples was emitted.
func TestSplitterRegressionOldBufferingFailure(t *testing.T) {
	const blockSize = 96_000
	const readSize = 8_192
	const numReads = 13 // 13 * 8192 = 106,496 samples

	// 1. Verify fixed implementation behavior
	splitter := newPCMBlockSplitter(blockSize)
	var emittedDuringPush []analyze.PCMBlock
	var allEmitted []analyze.PCMBlock

	emit := func(b analyze.PCMBlock) error {
		allEmitted = append(allEmitted, b)
		return nil
	}

	for r := range numReads {
		chunk := make([]float32, readSize)
		for i := range chunk {
			chunk[i] = float32(r*readSize + i)
		}
		start := int64(r * readSize)
		countBefore := len(allEmitted)
		if err := splitter.Push(chunk, start, emit); err != nil {
			t.Fatalf("read %d: Push error=%v", r, err)
		}
		if len(allEmitted) > countBefore {
			emittedDuringPush = append(emittedDuringPush, allEmitted[countBefore:]...)
		}
	}

	// At sample 90112 + 8192 = 98304, read 11 crossed the 96,000 boundary.
	// The fixed implementation MUST have emitted exactly 1 block of exactly 96,000 samples during Push!
	if len(emittedDuringPush) != 1 {
		t.Fatalf("emitted %d blocks during Push, want exactly 1 (old bug emitted 0 blocks because 98,304 != 96,000)",
			len(emittedDuringPush))
	}
	if len(emittedDuringPush[0].Samples) != blockSize {
		t.Fatalf("block 0 size=%d, want exactly %d", len(emittedDuringPush[0].Samples), blockSize)
	}
	if emittedDuringPush[0].StartSample != 0 {
		t.Fatalf("block 0 StartSample=%d, want 0", emittedDuringPush[0].StartSample)
	}

	if err := splitter.Flush(emit); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	// Final state: 2 blocks (96,000 + 10,496)
	if len(allEmitted) != 2 {
		t.Fatalf("total emitted blocks=%d, want 2", len(allEmitted))
	}
	if len(allEmitted[1].Samples) != 10_496 {
		t.Fatalf("final block size=%d, want 10,496", len(allEmitted[1].Samples))
	}
	if allEmitted[1].StartSample != blockSize {
		t.Fatalf("final block StartSample=%d, want %d", allEmitted[1].StartSample, blockSize)
	}

	// 2. Explicitly demonstrate that the OLD algorithm fails this requirement
	oldBuffer := make([]float32, 0, blockSize)
	var oldEmittedDuringPush []int
	for range numReads {
		chunk := make([]float32, readSize)
		oldBuffer = append(oldBuffer, chunk...)
		// Old condition: if len(buffer) == blockSamples
		if len(oldBuffer) == blockSize {
			oldEmittedDuringPush = append(oldEmittedDuringPush, len(oldBuffer))
			oldBuffer = make([]float32, 0, blockSize)
		}
	}
	// Verify that old algorithm failed: 0 blocks emitted during push, buffer grew unbounded
	if len(oldEmittedDuringPush) != 0 {
		t.Fatalf("old algorithm unexpectedly matched blockSamples")
	}
	if len(oldBuffer) != numReads*readSize {
		t.Fatalf("old buffer len=%d, want %d", len(oldBuffer), numReads*readSize)
	}
}

// Bounded memory test:
// Stream 10,000,000 samples (~40 MB) and verify splitter never holds more than blockSize samples.
func TestSplitterBoundedMemoryLongStream(t *testing.T) {
	const blockSize = 96_000
	const chunkSize = 8_192
	const totalSamples = 10_000_000

	splitter := newPCMBlockSplitter(blockSize)
	totalEmitted := int64(0)
	blocksCount := 0

	emit := func(b analyze.PCMBlock) error {
		blocksCount++
		totalEmitted += int64(len(b.Samples))
		if blocksCount <= totalSamples/blockSize {
			if len(b.Samples) != blockSize {
				return fmt.Errorf("block %d len=%d, want %d", blocksCount-1, len(b.Samples), blockSize)
			}
		}
		return nil
	}

	chunk := make([]float32, chunkSize)
	for i := range chunk {
		chunk[i] = 0.01
	}

	for cursor := int64(0); cursor < totalSamples; {
		readCount := chunkSize
		if cursor+int64(readCount) > totalSamples {
			readCount = int(totalSamples - cursor)
		}
		if err := splitter.Push(chunk[:readCount], cursor, emit); err != nil {
			t.Fatalf("Push error: %v", err)
		}
		// Invariant: buffered samples must ALWAYS be strictly less than blockSize
		if buffered := splitter.BufferedSamples(); buffered >= blockSize {
			t.Fatalf("splitter buffer size=%d exceeded blockSize=%d", buffered, blockSize)
		}
		cursor += int64(readCount)
	}

	if err := splitter.Flush(emit); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	expectedBlocks := (totalSamples + blockSize - 1) / blockSize
	if blocksCount != expectedBlocks {
		t.Fatalf("blocks emitted=%d, want %d", blocksCount, expectedBlocks)
	}
	if totalEmitted != totalSamples {
		t.Fatalf("total emitted samples=%d, want %d", totalEmitted, totalSamples)
	}
}

// Race safety and independent slice ownership test
func TestSplitterRaceAndOwnership(t *testing.T) {
	const blockSize = 96_000
	const chunkSize = 8_192
	const totalSamples = 500_000

	splitter := newPCMBlockSplitter(blockSize)
	blocksChan := make(chan analyze.PCMBlock, 4)

	var wg sync.WaitGroup
	// 4 workers consuming and validating blocks concurrently
	for range 4 {
		wg.Go(func() {
			for b := range blocksChan {
				sum := float64(0)
				for _, sample := range b.Samples {
					sum += float64(sample)
				}
				if math.IsNaN(sum) {
					t.Errorf("NaN encountered in worker")
				}
			}
		})
	}

	emit := func(b analyze.PCMBlock) error {
		blocksChan <- b
		return nil
	}

	chunk := make([]float32, chunkSize)
	for i := range chunk {
		chunk[i] = float32(i%100) * 0.01
	}

	for offset := int64(0); offset < totalSamples; offset += chunkSize {
		end := min(offset+chunkSize, totalSamples)
		thisChunk := chunk[:end-offset]
		if err := splitter.Push(thisChunk, offset, emit); err != nil {
			t.Fatalf("Push error: %v", err)
		}
	}
	if err := splitter.Flush(emit); err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	close(blocksChan)
	wg.Wait()
}

// Unexpected discontinuity detection
func TestSplitterDiscontinuityDetection(t *testing.T) {
	splitter := newPCMBlockSplitter(96_000)
	emit := func(b analyze.PCMBlock) error { return nil }

	chunk := make([]float32, 1000)
	if err := splitter.Push(chunk, 0, emit); err != nil {
		t.Fatalf("initial Push failed: %v", err)
	}
	// Expected next start is 1000, but decoder reports 2000
	err := splitter.Push(chunk, 2000, emit)
	if err == nil {
		t.Fatal("expected discontinuity error, got nil")
	}
	if !strings.Contains(err.Error(), "discontinuity") {
		t.Fatalf("error %q does not mention discontinuity", err)
	}
}

// Cancellation stops Push immediately
func TestSplitterCancellation(t *testing.T) {
	splitter := newPCMBlockSplitter(1000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	emit := func(b analyze.PCMBlock) error {
		return ctx.Err()
	}

	chunk := make([]float32, 2000)
	err := splitter.Push(chunk, 0, emit)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Push error=%v, want context.Canceled", err)
	}
}
